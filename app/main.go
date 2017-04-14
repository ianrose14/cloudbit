package fs

import (
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/appengine"
	"google.golang.org/appengine/datastore"
	"google.golang.org/appengine/log"
	"google.golang.org/appengine/urlfetch"
)

/*

The plan:

1 endpoint (that we hit manually) called /setup which will set up the subscriptions and all that we need
1 endpoint called /events which is what the littebit will call whenever the littebit input changes

List subscriptions:
curl -i -H "Authorization: Bearer ba5a005d3e33b108258c5d0abf432267a561fc180a594a29d57aa4f0c6c24eee" -H "Accept: application/vnd.littlebits.v2+json" https://api-http.littlebitscloud.cc/subscriptions?publisher_id=00e04c1efff8

Create a subscription:
curl -H "Content-Type: application/json" --data '{"publisher_id": "00e04c1efff8", "subscriber_id": "https://littlebits-rose.appspot.com/events", "publisher_events": ["amplitude"]}'  -X POST -i -H "Authorization: Bearer ba5a005d3e33b108258c5d0abf432267a561fc180a594a29d57aa4f0c6c24eee" -H "Accept: application/vnd.littlebits.v2+json" https://api-http.littlebitscloud.cc/subscriptions

Delete a subscription:
curl -d publisher_id=00e04c1efff8 -d subscriber_id="https://littlebits-rose.appspot.com/events" -X DELETE -i -H "Authorization: Bearer ba5a005d3e33b108258c5d0abf432267a561fc180a594a29d57aa4f0c6c24eee" -H "Accept: application/vnd.littlebits.v2+json" 'https://api-http.littlebitscloud.cc/subscriptions'

*/

const (
	// TODO(ianrose): move to a datastore entity
	accessToken = "ba5a005d3e33b108258c5d0abf432267a561fc180a594a29d57aa4f0c6c24eee"

	// get this value from a GET request to https://api-http.littlebitscloud.cc/devices
	deviceId = "00e04c1efff8"
)

type DeviceEvent struct {
	DeviceId  string `json:"device_id"`
	UserId    int64  `json:"user_id"`
	Timestamp int64  `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Absolute string `json:"absolute"` // TODO - what is the type?
	}
	//{ device_id:"000001", user_id:<Int>, timestamp:<Int>, type:'amplitude', payload: {absolute:*, percent:*, delta:'ignite', level:*}}
}

type BirthdayMsg struct {
	Created  time.Time
	Url      string
	Duration time.Duration
}

var (
	messages = []BirthdayMsg{
		{time.Time{}, "https://www.youtube.com/watch?v=glNjsOHiBYs", 32 * time.Second},
		{time.Time{}, "https://www.youtube.com/watch?v=-M3v8NXhPD4", 11 * time.Second},
		{time.Time{}, "https://youtu.be/FQwdy2wPpjU?t=8s", 30 * time.Second},
	}
)

func init() {
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/events", eventsHandler)
	http.HandleFunc("/poll", pollHandler)
	http.HandleFunc("/stop", stopHandler)
	http.HandleFunc("/setup", setupHandler)
	http.Handle("/static", http.FileServer(http.Dir("static")))
}

func failAndLog(ctx context.Context, w http.ResponseWriter, code int, format string, args ...interface{}) {
	if code >= 500 {
		log.Errorf(ctx, format, args...)
	} else {
		log.Warningf(ctx, format, args...)
	}
	http.Error(w, fmt.Sprintf(format, args...), code)
}

func eventsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := appengine.NewContext(r)

	if r.Method != "POST" {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	// actually don't care about payload contents since we only listen for "amplitude:delta:ignite" events

	rand.Seed(time.Now().UnixNano())
	i := rand.Intn(len(messages))

	msg := messages[i]
	msg.Created = time.Now()
	key := datastore.NewKey(ctx, "BirthdayMsg", "global", 0, nil)
	if _, err := datastore.Put(ctx, key, &msg); err != nil {
		failAndLog(ctx, w, http.StatusInternalServerError, "failed to put %s to datastore: %s", key, err)
		return
	}

	sendOutputToDevice(ctx, w, 100, 32000)
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	page := `<html>
	<body>
	<p><a href="/static/poller.html">poller</a></p>
	<p><a href="/setup">setup</a></p>
	<p><a href="/stop">stop</a></p>
	</body>
	</html>`
	fmt.Fprintln(w, page)
}

func pollHandler(w http.ResponseWriter, r *http.Request) {
	ctx := appengine.NewContext(r)

	var msg BirthdayMsg
	key := datastore.NewKey(ctx, "BirthdayMsg", "global", 0, nil)

	if err := datastore.Get(ctx, key, &msg); err != nil {
		if err == datastore.ErrNoSuchEntity {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"found": false}`)
		} else {
			failAndLog(ctx, w, http.StatusInternalServerError, "failed to get %s: %s", key, err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"found": true, "url": %q, "durationMs": %d, "created": %q}`, msg.Url, int(msg.Duration/time.Millisecond), msg.Created.Format(time.RFC3339))
}

func setupHandler(w http.ResponseWriter, r *http.Request) {
	ctx := appengine.NewContext(r)

	payload := `{"publisher_id": "00e04c1efff8", "subscriber_id": "https://littlebits-rose.appspot.com/events", "publisher_events": ["amplitude:delta:ignite"]}`

	req, err := http.NewRequest("POST", "https://api-http.littlebitscloud.cc/subscriptions", strings.NewReader(payload))
	if err != nil {
		failAndLog(ctx, w, http.StatusInternalServerError, "failed to make request: %s", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Add("Accept", "application/vnd.littlebits.v2+json")

	if err := doLittleBitsRequest(ctx, req); err != nil {
		failAndLog(ctx, w, http.StatusInternalServerError, "failed to create new subscription: %s", err)
		return
	}

	fmt.Fprintf(w, "ok!")
}

func stopHandler(w http.ResponseWriter, r *http.Request) {
	ctx := appengine.NewContext(r)
	sendOutputToDevice(ctx, w, 0, -1)

	key := datastore.NewKey(ctx, "BirthdayMsg", "global", 0, nil)
	if err := datastore.Delete(ctx, key); err != nil {
		log.Errorf(ctx, "failed to delete %s: %s", key, err)
	}
}

func doLittleBitsRequest(ctx context.Context, req *http.Request) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Add("Accept", "application/vnd.littlebits.v2+json")

	client := urlfetch.Client(ctx)
	rsp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to do %s request: %s", req.URL.Path, err)
	}

	io.Copy(ioutil.Discard, rsp.Body)
	rsp.Body.Close()

	if rsp.StatusCode > 299 {
		return fmt.Errorf("failed to do %s request: %s", req.URL.Path, rsp.Status)
	}

	return nil
}

func sendOutputToDevice(ctx context.Context, w http.ResponseWriter, percent int, durationMs int) {
	payload := fmt.Sprintf(`{"percent": %d, "duration_ms": %d}`, percent, durationMs)
	url := fmt.Sprintf("https://api-http.littlebitscloud.cc/v2/devices/%s/output", deviceId)

	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		failAndLog(ctx, w, http.StatusInternalServerError, "failed to make request: %s", err)
		return
	}

	if err := doLittleBitsRequest(ctx, req); err != nil {
		failAndLog(ctx, w, http.StatusInternalServerError, "failed to send output to device: %s", err)
		return
	}

	fmt.Fprintf(w, "ok!")
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

	urls := fmt.Sprintf("https://api-http.littlebitscloud.cc/devices/%s/output", deviceId)
	params := url.Values{}
	params.Add("percent", "100")
	params.Add("duration_ms", "1000")

	req, err := http.NewRequest("POST", urls, strings.NewReader(params.Encode()))
	if err != nil {
		failAndLog(ctx, w, http.StatusInternalServerError, "failed to create http request: %s", err)
		return
	}
	req.Header.Add("Accept", "application/vnd.littlebits.v2+json") // use API v2
	req.Header.Add("Authorization", "Bearer "+accessToken)
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
