package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/appengine"
	"google.golang.org/appengine/channel"
	"google.golang.org/appengine/datastore"
	"google.golang.org/appengine/log"
	"google.golang.org/appengine/urlfetch"
)

const (
	ChannelInfoKind = "ChannelInfo"

	// Fixed datastore ID of the (singleton) ChannelInfo datastore entity.
	ChannelInfoId = "avalon" // arbitrary word starting with A, to denote "the first channel"
)

type ChannelInfo struct {
	Created    time.Time `datastore:",noindex"`
	RemoteAddr string    `datastore:",noindex"`
	ClientId   string    `datastore:",noindex"`
}

type PushNotification struct {
	Msg string `json:"msg"`
}

type RegisterResponse struct {
	Token string `json:"token"`
}

// Describes the config/tokens.json file
type Tokens struct {
	AccessToken string `json:"access_token"`
	DeviceId    string `json:"device_id"`
}

func init() {
	http.HandleFunc("/ping", pingHandler)
	http.HandleFunc("/register", registerHandler)
	http.HandleFunc("/test", testHandler)
}

func failAndLog(ctx context.Context, w http.ResponseWriter, code int, format string, args ...interface{}) {
	if code >= 500 {
		log.Errorf(ctx, format, args...)
	} else {
		log.Warningf(ctx, format, args...)
	}
	http.Error(w, fmt.Sprintf(format, args...), code)
}

func pingHandler(w http.ResponseWriter, r *http.Request) {
	ctx := appengine.NewContext(r)

	key := datastore.NewKey(ctx, ChannelInfoKind, ChannelInfoId, 0, nil)
	var channelInfo ChannelInfo
	if err := datastore.Get(ctx, key, &channelInfo); err != nil {
		failAndLog(ctx, w, http.StatusInternalServerError, "failed to get %s from datastore", key)
		return
	}

	log.Debugf(ctx, "fetched %s from datastore", key)

	payload := PushNotification{
		Msg: "hi!",
	}

	if err := channel.SendJSON(ctx, channelInfo.ClientId, &payload); err != nil {
		log.Errorf(ctx, "failed to send channel notification to %q: %s", channelInfo.ClientId, err)
	}
}

func registerHandler(w http.ResponseWriter, r *http.Request) {
	ctx := appengine.NewContext(r)

	clientId := r.URL.Query().Get("clientId")
	if clientId == "" {
		failAndLog(ctx, w, http.StatusBadRequest, "missing required query param \"clientId\"")
		return
	}

	key := datastore.NewKey(ctx, ChannelInfoKind, ChannelInfoId, 0, nil)
	channelInfo := ChannelInfo{
		Created:    time.Now(),
		RemoteAddr: r.RemoteAddr,
		ClientId:   clientId,
	}

	if _, err := datastore.Put(ctx, key, &channelInfo); err != nil {
		failAndLog(ctx, w, http.StatusInternalServerError, "failed to put %s to datastore: %s", key, err)
		return
	}

	token, err := channel.Create(ctx, clientId)
	if err != nil {
		failAndLog(ctx, w, http.StatusInternalServerError, "failed to create channel %q: %s", clientId, err)
		return
	}

	log.Debugf(ctx, "created channel %q with token %s", clientId, token)

	rsp := RegisterResponse{
		Token: token,
	}

	w.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(w).Encode(&rsp); err != nil {
		failAndLog(ctx, w, http.StatusInternalServerError, "failed to write json to response: %s", err)
		return
	}
}

func testHandler(w http.ResponseWriter, r *http.Request) {
	ctx := appengine.NewContext(r)

	fp, err := os.Open("config/tokens.json")
	if err != nil {
		if os.IsNotExist(err) {
			failAndLog(ctx, w, http.StatusNotFound, "failed to open tokens file: %s", err)
		} else {
			failAndLog(ctx, w, http.StatusInternalServerError, "failed to open tokens file: %s", err)
		}
		return
	}
	defer fp.Close()

	var tokens Tokens
	if err := json.NewDecoder(fp).Decode(&tokens); err != nil {
		failAndLog(ctx, w, http.StatusBadRequest, "failed to parse json request: %s", err)
		return
	}

	urls := fmt.Sprintf("https://api-http.littlebitscloud.cc/devices/%s/output", tokens.DeviceId)
	params := url.Values{}
	params.Add("percent", "100")
	params.Add("duration_ms", "1000")

	req, err := http.NewRequest("POST", urls, strings.NewReader(params.Encode()))
	if err != nil {
		failAndLog(ctx, w, http.StatusInternalServerError, "failed to create http request: %s", err)
		return
	}
	req.Header.Add("Accept", "application/vnd.littlebits.v2+json") // use API v2
	req.Header.Add("Authorization", "Bearer "+tokens.AccessToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := urlfetch.Client(ctx)
	rsp, err := client.Do(req)
	if err != nil {
		failAndLog(ctx, w, http.StatusInternalServerError, "failed to contact LittleBits server: %s", err)
		return
	}

	defer func() {
		io.Copy(ioutil.Discard, r.Body) // drain...
		r.Body.Close()                  // ...and close
	}()

	if rsp.StatusCode != http.StatusOK {
		s, err := ioutil.ReadAll(rsp.Body)
		if err != nil {
			log.Warningf(ctx, "failed to read error response body: %s", err)
			s = nil
		}
		failAndLog(ctx, w, rsp.StatusCode, "failed to POST output voltage to LittleBits device (%d): %s", rsp.StatusCode, strings.TrimSpace(string(s)))
		return
	}

	// note: there is nothing interesting in the response body

	log.Debugf(ctx, "successful post to %s", urls)

	fmt.Fprintf(w, "successfully wrote: %s\n", params.Encode())
}
