package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/interlink-hq/interlink/pkg/interlink"
)

const testUID = "9f60656e-6a25-4ad1-8b15-56ded9771090"

func createBody(text string) string {
	body, _ := json.Marshal(map[string]any{
		"pod": map[string]any{
			"metadata": map[string]any{"uid": testUID, "name": "text", "namespace": "default",
				"annotations": map[string]string{consentAnnotation: "true", textAnnotation: text}},
			"spec": map[string]any{"containers": []map[string]any{{"name": "words",
				"env":     []map[string]string{{"name": "PRIVATE", "value": "never-forward-env"}},
				"command": []string{"never-execute"}}}},
		},
		"container": []map[string]any{{"secrets": []map[string]string{{"data": "never-forward-volume"}}}},
		"jobScript": "never-forward-script",
	})
	return string(body)
}

func call(t *testing.T, handler http.Handler, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	if w.Code != want {
		t.Fatalf("%s %s: code %d, want %d; body=%s", method, path, w.Code, want, w.Body)
	}
	return w
}

func TestAPIAndAggregation(t *testing.T) {
	g := newGateway()
	api := g.api()
	body := createBody(strings.Repeat("Hello world ", 150) + "constructor __proto__")
	w := call(t, api, "POST", "/create", body, http.StatusOK)
	var created interlink.CreateStruct
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.PodUID != testUID || created.PodJID == "" {
		t.Fatal("wrong create response")
	}
	duplicate := call(t, api, "POST", "/create", body, http.StatusOK)
	if duplicate.Body.String() != w.Body.String() || len(g.jobs) != 1 {
		t.Fatal("create is not idempotent")
	}
	query := fmt.Sprintf(`[{"metadata":{"uid":%q}}]`, testUID)
	w = call(t, api, "GET", "/status", query, http.StatusOK)
	if !strings.Contains(w.Body.String(), "WaitingForBrowser") {
		t.Fatal(w.Body.String())
	}
	if call(t, api, "GET", "/status", "[]", http.StatusOK).Body.String() != "[]\n" {
		t.Fatal("ping response")
	}

	// Complete the second chunk first to check order-independent aggregation.
	j, first := g.assign("one", time.Now())
	_, second := g.assign("two", time.Now())
	if first == nil || second == nil || g.assignableCount() != 0 {
		t.Fatal("chunk assignment failed")
	}
	w = call(t, api, "GET", "/status", query, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"running"`) {
		t.Fatal(w.Body.String())
	}
	for _, item := range []struct {
		c      *chunk
		worker string
	}{{second, "two"}, {first, "one"}} {
		res := result{Type: "result", Attempt: item.c.attempt, Counts: countText(item.c.text)}
		if !g.accept(j, item.c, item.worker, res, time.Now()) {
			t.Fatal("valid result rejected")
		}
		if g.accept(j, item.c, item.worker, res, time.Now()) {
			t.Fatal("duplicate accepted")
		}
	}
	w = call(t, api, "GET", "/status", query, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"exitCode":0`) || !strings.Contains(w.Body.String(), "Completed") {
		t.Fatal(w.Body.String())
	}
	logRequest := fmt.Sprintf(`{"PodUID":%q,"ContainerName":"words","Opts":{}}`, testUID)
	w = call(t, api, "GET", "/getLogs", logRequest, http.StatusOK)
	want := "state=Completed chunks=2/2 workers=0\nconstructor: 1\nhello: 150\nproto: 1\nworld: 150\n"
	if w.Body.String() != want {
		t.Fatalf("logs=%q", w.Body.String())
	}
	tail := strings.Replace(logRequest, `"Opts":{}`, `"Opts":{"Tail":1}`, 1)
	if call(t, api, "GET", "/getLogs", tail, http.StatusOK).Body.String() != "world: 150\n" {
		t.Fatal("tail")
	}
	limited := strings.Replace(logRequest, `"Opts":{}`, `"Opts":{"Bytes":5}`, 1)
	if call(t, api, "GET", "/getLogs", limited, http.StatusOK).Body.Len() != 5 {
		t.Fatal("bytes")
	}
	unsupported := strings.Replace(logRequest, `"Opts":{}`, `"Opts":{"Follow":true}`, 1)
	call(t, api, "GET", "/getLogs", unsupported, http.StatusBadRequest)
	call(t, api, "POST", "/delete", fmt.Sprintf(`{"metadata":{"uid":%q}}`, testUID), http.StatusOK)
	call(t, api, "POST", "/delete", fmt.Sprintf(`{"metadata":{"uid":%q}}`, testUID), http.StatusOK)
	call(t, api, "GET", "/getLogs", logRequest, http.StatusNotFound)
	if len(g.jobs) != 0 || len(g.order) != 0 {
		t.Fatal("delete retained job")
	}
}

func (g *gateway) assignableCount() int {
	n := 0
	for _, j := range g.jobs {
		for _, c := range j.chunks {
			if !c.done && c.worker == "" {
				n++
			}
		}
	}
	return n
}

func countText(text string) map[string]int {
	counts := make(map[string]int)
	for _, word := range words.FindAllString(strings.ToLower(text), -1) {
		counts[word]++
	}
	return counts
}

func TestBoundsAndIsolation(t *testing.T) {
	for _, text := range []string{"", "☃", strings.Repeat("a", 65), strings.Repeat("a ", maxTextBytes)} {
		call(t, newGateway().api(), "POST", "/create", createBody(text), http.StatusBadRequest)
	}
	g := newGateway()
	for _, body := range []string{
		strings.Replace(createBody("hi"), `"true"`, `"false"`, 1),
		strings.Replace(createBody("hi"), `"containers":`, `"initContainers":[{}],"containers":`, 1),
		strings.Replace(createBody("hi"), `"uid":`+fmt.Sprintf("%q", testUID), `"uid":""`, 1),
		createBody("hi") + "{}",
		strings.Repeat(" ", maxRequestBytes) + createBody("hi"),
	} {
		call(t, g.api(), "POST", "/create", body, http.StatusBadRequest)
	}
	req := httptest.NewRequest("POST", "/create", strings.NewReader(createBody("hi")))
	req.Header.Set("Origin", "https://untrusted.example")
	w := httptest.NewRecorder()
	g.api().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatal("browser access to private API allowed")
	}
	call(t, g.public(), "POST", "/create", createBody("hi"), http.StatusMethodNotAllowed)
	call(t, g.public(), "GET", "/ws", "", http.StatusForbidden)
	call(t, g.api(), "POST", "/create", createBody("hi"), http.StatusOK)
	_, c := g.assign("worker", time.Now())
	message, _ := json.Marshal(task{Type: "task", Attempt: c.attempt, Text: c.text})
	for _, private := range []string{testUID, "never-forward", "annotations", "env", "jobScript"} {
		if strings.Contains(string(message), private) {
			t.Fatalf("forwarded private field %q", private)
		}
	}
	for i := 1; i < maxJobs; i++ {
		call(t, g.api(), "POST", "/create", strings.ReplaceAll(createBody("hi"), testUID, fmt.Sprint(i)), http.StatusOK)
	}
	call(t, g.api(), "POST", "/create", strings.ReplaceAll(createBody("hi"), testUID, "overflow"), http.StatusServiceUnavailable)
}

func TestLeasesInvalidResultsAndExpiry(t *testing.T) {
	g := newGateway()
	call(t, g.api(), "POST", "/create", createBody("a b a"), http.StatusOK)
	now := time.Now()
	j, c := g.assign("one", now)
	original := c.attempt
	for _, counts := range []map[string]int{{"a": -1, "b": 4}, {"injected": 3}, {"a": 1}, {"a": 999999}} {
		if g.accept(j, c, "one", result{Type: "result", Attempt: original, Counts: counts}, now) {
			t.Fatal("invalid result accepted")
		}
	}
	valid := result{Type: "result", Attempt: original, Counts: countText(c.text)}
	if g.accept(j, c, "other", valid, now) || g.accept(j, c, "one", valid, c.deadline) {
		t.Fatal("invalid lease accepted")
	}
	c.worker = "" // Disconnect releases the lease.
	g.assign("two", now)
	if g.accept(j, c, "two", valid, now) {
		t.Fatal("late result accepted")
	}
	c.worker = ""
	g.assign("three", now)
	c.worker = ""
	g.assign("four", now)
	if j.reason != "AttemptsExceeded" {
		t.Fatal("retry limit not enforced")
	}
	g.sweep(j.finished.Add(retention))
	if len(g.jobs) != 0 || len(g.order) != 0 {
		t.Fatal("retention not enforced")
	}

	call(t, g.api(), "POST", "/create", createBody("a"), http.StatusOK)
	j = g.jobs[testUID]
	g.sweep(j.created.Add(jobDuration))
	if j.reason != "DeadlineExceeded" {
		t.Fatal("queued job deadline not enforced")
	}
}

func TestWebSocketDisconnectAndCompletion(t *testing.T) {
	g := newGateway()
	call(t, g.api(), "POST", "/create", createBody("browser browser demo"), http.StatusOK)
	server := httptest.NewServer(g.public())
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?consent=yes"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var first task
	if err := conn.ReadJSON(&first); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	conn, _, err = websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var second task
	if err := conn.ReadJSON(&second); err != nil {
		t.Fatal(err)
	}
	if second.Attempt == first.Attempt || second.Text != first.Text {
		t.Fatal("reassignment failed")
	}
	if err := conn.WriteJSON(result{Type: "result", Attempt: first.Attempt, Counts: countText(first.Text)}); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(result{Type: "result", Attempt: second.Attempt, Counts: countText(second.Text)}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		g.mu.Lock()
		completed := g.jobs[testUID].reason == "Completed"
		g.mu.Unlock()
		if completed {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("browser result never completed job")
}
