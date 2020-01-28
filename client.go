package tonlib

//#cgo linux CFLAGS: -I./lib/linux
//#cgo darwin CFLAGS: -I./lib/darwin
//#cgo linux LDFLAGS: -L./lib/linux -ltonlibjson -ltonlibjson_private -ltonlibjson_static -ltonlib
//#cgo darwin LDFLAGS: -L./lib/darwin -ltonlibjson -ltonlibjson_private -ltonlibjson_static -ltonlib
//#include <stdlib.h>
//#include <./lib/tonlib_client_json.h>
import "C"
import (
	"encoding/json"
	"fmt"
	"math/rand"
	"time"
	"unsafe"
)

const (
	DEFAULT_TIMEOUT = 4.5
	DefaultRetries  = 10
)

type InputKey struct {
	Type          string        `json:"@type"`
	LocalPassword string        `json:"local_password"`
	Key           TONPrivateKey `json:"key"`
}
type TONPrivateKey struct {
	PublicKey string `json:"public_key"`
	Secret    string `json:"secret"`
}

type SyncState struct {
	Type         string `json:"@type"`
	FromSeqno    int    `json:"from_seqno"`
	ToSeqno      int    `json:"to_seqno"`
	CurrentSeqno int    `json:"current_seqno"`
}

// KeyStoreType directory
type KeyStoreType struct {
	Type      string `json:"@type"`
	Directory string `json:"directory"`
}

// TONResponse alias for use in TONResult
type TONResponse map[string]interface{}

// TONResult is used to unmarshal received json strings into
type TONResult struct {
	Data TONResponse
	Raw  []byte
}

// Client is the Telegram TdLib client
type Client struct {
	client unsafe.Pointer
	config Config
	// wallet *TonWallet
}

type TonInitRequest struct {
	Type    string  `json:"@type"`
	Options Options `json:"options"`
}

// NewClient Creates a new instance of TONLib.
func NewClient(tonCnf *TonInitRequest, config Config) (*Client, error) {
	rand.Seed(time.Now().UnixNano())

	client := Client{client: C.tonlib_client_json_create(), config: config}
	optionsInfo, err := client.Init(&tonCnf.Options)
	//resp, err := client.executeAsynchronously(tonCnf)
	if err != nil {
		return &client, err
	}
	if optionsInfo.tonCommon.Type == "ok" {
		return &client, nil
	}
	if optionsInfo.tonCommon.Type == "error" {
		return &client, fmt.Errorf("Error ton client init. Message: %s. ", optionsInfo.tonCommon.Extra)
	}
	return &client, fmt.Errorf("Error ton client init. ")
}

/**
execute ton-lib asynchronously
*/
func (client *Client) executeAsynchronously(data interface{}) (*TONResult, error) {
	req, err := json.Marshal(data)
	if err != nil {
		return &TONResult{}, err
	}
	cs := C.CString(string(req))
	defer C.free(unsafe.Pointer(cs))

	fmt.Println("call", string(req))
	C.tonlib_client_json_send(client.client, cs)
	result := C.tonlib_client_json_receive(client.client, DEFAULT_TIMEOUT)

	num := 0
	for result == nil {
		if num >= DefaultRetries{
			return &TONResult{}, fmt.Errorf("Client.executeAsynchronously: exided limit of retries to get json response from TON C`s lib. ")
		}
		time.Sleep(1 * time.Second)
		result = C.tonlib_client_json_receive(client.client, DEFAULT_TIMEOUT)
		num += 1
	}

	var updateData TONResponse
	res := C.GoString(result)
	resB := []byte(res)
	err = json.Unmarshal(resB, &updateData)
	fmt.Println("fetch data: ", string(resB))
	if st, ok := updateData["@type"]; ok && st == "updateSendLiteServerQuery" {
		err = json.Unmarshal(resB, &updateData)
		if err == nil {
			_, err = client.OnLiteServerQueryResult(updateData["data"].([]byte), updateData["id"].(JSONInt64),)
		}
	}
	if st, ok := updateData["@type"]; ok && st == "updateSyncState" {
		syncResp := struct {
			Type      string    `json:"@type"`
			SyncState SyncState `json:"sync_state"`
		}{}
		err = json.Unmarshal(resB, &syncResp)
		if err != nil {
			return &TONResult{}, err
		}
		fmt.Println("run sync", updateData)
		res, err = client.Sync(syncResp.SyncState)
		if err != nil {
			return &TONResult{}, err
		}
		if res != "" {
			// parse and return reponse that has been catched while sync
			resB := []byte(res)
			err = json.Unmarshal(resB, &updateData)
			if err != nil {
				return &TONResult{}, err
			}
			return &TONResult{Data: updateData, Raw: resB}, err
		}
		return client.executeAsynchronously(data)
	}
	return &TONResult{Data: updateData, Raw: resB}, err
}

/**
execute ton-lib synchronously
*/
func (client *Client) executeSynchronously(data interface{}) (*TONResult, error) {
	req, _ := json.Marshal(data)
	cs := C.CString(string(req))
	defer C.free(unsafe.Pointer(cs))
	result := C.tonlib_client_json_execute(client.client, cs)

	var updateData TONResponse
	res := C.GoString(result)
	resB := []byte(res)
	err := json.Unmarshal(resB, &updateData)
	return &TONResult{Data: updateData, Raw: resB}, err
}

func (client *Client) Destroy() {
	C.tonlib_client_json_destroy(client.client)
}

//sync node`s blocks to current
func (client *Client) Sync(syncState SyncState) (string, error){
	data := struct {
		Type      string       `json:"@type"`
		SyncState SyncState `json:"sync_state"`
	}{
		Type: "sync",
		SyncState: syncState,
	}
	req, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	cs := C.CString(string(req))
	defer C.free(unsafe.Pointer(cs))
	C.tonlib_client_json_send(client.client, cs)
	for {
		result := C.tonlib_client_json_receive(client.client, DEFAULT_TIMEOUT)
		for result == nil {
			fmt.Println("empty response. next attempt")
			time.Sleep(1 * time.Second)
			result = C.tonlib_client_json_receive(client.client, DEFAULT_TIMEOUT)
		}
		syncResp := struct {
			Type      string    `json:"@type"`
			SyncState SyncState `json:"sync_state"`
		}{}
		res := C.GoString(result)
		resB := []byte(res)
		err = json.Unmarshal(resB, &syncResp)
		fmt.Println("sync result #1: ", res)
		if err != nil {
			return "", err
		}
		if syncResp.Type == "error"{
			return "", fmt.Errorf("Got an error response from ton: `%s` ", res)
		}
		if syncResp.SyncState.Type == "syncStateDone" {
			result := C.tonlib_client_json_receive(client.client, DEFAULT_TIMEOUT)
			syncResp = struct {
				Type      string    `json:"@type"`
				SyncState SyncState `json:"sync_state"`
			}{}
			res := C.GoString(result)
			resB := []byte(res)
			err = json.Unmarshal(resB, &syncResp)
			fmt.Println("sync result #2: ", string(resB))
			if err != nil {
				return "", err
			}
		}
		if syncResp.Type == "ok" {
			return "", nil
		}
		if syncResp.Type == "updateSyncState"{
			// continue updating
			continue
		}
		// response on previously not sync request
		return res, nil
	}
}

// key struct cause it strings values no bytes
// Key
type Key struct {
	tonCommon
	PublicKey string       `json:"public_key"` //
	Secret    string `json:"secret"`     //
}

// MessageType return the string telegram-type of Key
func (key *Key) MessageType() string {
	return "key"
}

// NewKey creates a new Key
//
// @param publicKey
// @param secret
func NewKey(publicKey string, secret string) *Key {
	keyTemp := Key{
		tonCommon: tonCommon{Type: "key"},
		PublicKey: publicKey,
		Secret:    secret,
	}

	return &keyTemp
}

// not bytes but string -exception
// SendGramsResult
type SendGramsResult struct {
	tonCommon
	BodyHash  string `json:"body_hash"`  //
	SentUntil int64  `json:"sent_until"` //
}

// MessageType return the string telegram-type of SendGramsResult
func (sendGramsResult *SendGramsResult) MessageType() string {
	return "sendGramsResult"
}

// NewSendGramsResult creates a new SendGramsResult
//
// @param bodyHash
// @param sentUntil
func NewSendGramsResult(bodyHash string, sentUntil int64) *SendGramsResult {
	sendGramsResultTemp := SendGramsResult{
		tonCommon: tonCommon{Type: "sendGramsResult"},
		BodyHash:  bodyHash,
		SentUntil: sentUntil,
	}

	return &sendGramsResultTemp
}

// RawMessage
type RawMessage struct {
	tonCommon
	BodyHash    string    `json:"body_hash"`   //
	CreatedLt   JSONInt64 `json:"created_lt"`  //
	Destination string    `json:"destination"` //
	FwdFee      JSONInt64 `json:"fwd_fee"`     //
	IhrFee      JSONInt64 `json:"ihr_fee"`     //
	Message     string    `json:"message"`     //
	Source      string    `json:"source"`      //
	Value       JSONInt64 `json:"value"`       //
}

// MessageType return the string telegram-type of RawMessage
func (rawMessage *RawMessage) MessageType() string {
	return "raw.message"
}

// NewRawMessage creates a new RawMessage
//
// @param bodyHash
// @param createdLt
// @param destination
// @param fwdFee
// @param ihrFee
// @param message
// @param source
// @param value
func NewRawMessage(bodyHash string, createdLt JSONInt64, destination string, fwdFee JSONInt64, ihrFee JSONInt64, message string, source string, value JSONInt64) *RawMessage {
	rawMessageTemp := RawMessage{
		tonCommon:   tonCommon{Type: "raw.message"},
		BodyHash:    bodyHash,
		CreatedLt:   createdLt,
		Destination: destination,
		FwdFee:      fwdFee,
		IhrFee:      ihrFee,
		Message:     message,
		Source:      source,
		Value:       value,
	}

	return &rawMessageTemp
}