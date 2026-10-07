// Browser-demo is an in-memory text-only interLink plugin, not a container runtime.
package main

import (
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/interlink-hq/interlink/pkg/interlink"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	textAnnotation    = "browser-demo.interlink.eu/public-text"
	consentAnnotation = "browser-demo.interlink.eu/public"
	maxTextBytes      = 64 * 1024
	maxRequestBytes   = 256 * 1024
	chunkWords        = 256
	maxJobs           = 32
	maxWorkers        = 64
	maxAttempts       = 3
	leaseDuration     = 10 * time.Second
	jobDuration       = 2 * time.Minute
	retention         = 10 * time.Minute
)

//go:embed web/*
var assets embed.FS

var words = regexp.MustCompile(`[A-Za-z0-9]+`)

// Decode only the fields the demo needs; volume data, env, commands and images
// are never stored or sent to participants.
type podRef struct {
	Metadata struct {
		UID         string            `json:"uid"`
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec struct {
		Containers []struct {
			Name string `json:"name"`
		} `json:"containers"`
		InitContainers []json.RawMessage `json:"initContainers"`
	} `json:"spec"`
}

type chunk struct {
	text     string
	allowed  map[string]bool
	total    int
	attempt  string
	worker   string
	deadline time.Time
	tries    int
	done     bool
}

type job struct {
	uid, name, namespace, container, id string
	chunks                              []*chunk
	counts                              map[string]int
	created, started, finished          time.Time
	reason                              string
}

type gateway struct {
	mu      sync.Mutex
	jobs    map[string]*job
	order   []string
	workers int
}

func newGateway() *gateway {
	return &gateway{jobs: make(map[string]*job)}
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	d := json.NewDecoder(r.Body)
	if err := d.Decode(dst); err != nil {
		http.Error(w, "invalid or oversized JSON request", http.StatusBadRequest)
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		http.Error(w, "expected one JSON value", http.StatusBadRequest)
		return false
	}
	return true
}

func respond(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Print("response write failed")
	}
}

func makeChunks(text string) ([]*chunk, error) {
	if len(text) == 0 || len(text) > maxTextBytes || !utf8.ValidString(text) {
		return nil, errors.New("public-text must be valid UTF-8, nonempty, and at most 64 KiB")
	}
	tokens := words.FindAllString(text, -1)
	if len(tokens) == 0 {
		return nil, errors.New("public-text must contain ASCII words")
	}
	for i, token := range tokens {
		if len(token) > 64 {
			return nil, errors.New("ASCII words must be at most 64 characters")
		}
		tokens[i] = strings.ToLower(token)
	}
	var chunks []*chunk
	for start := 0; start < len(tokens); start += chunkWords {
		end := min(start+chunkWords, len(tokens))
		c := &chunk{text: strings.Join(tokens[start:end], " "), allowed: make(map[string]bool), total: end - start}
		for _, token := range tokens[start:end] {
			c.allowed[token] = true
		}
		chunks = append(chunks, c)
	}
	return chunks, nil
}

func (g *gateway) create(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Pod podRef `json:"pod"`
	}
	if !decode(w, r, &request) {
		return
	}
	p := request.Pod
	if p.Metadata.UID == "" || p.Metadata.Name == "" || p.Metadata.Namespace == "" ||
		len(p.Spec.Containers) != 1 || p.Spec.Containers[0].Name == "" || len(p.Spec.InitContainers) != 0 ||
		p.Metadata.Annotations[consentAnnotation] != "true" {
		http.Error(w, "requires pod identity, one container, no init containers, and public=true annotation", http.StatusBadRequest)
		return
	}
	chunks, err := makeChunks(p.Metadata.Annotations[textAnnotation])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweep(time.Now())
	if j := g.jobs[p.Metadata.UID]; j != nil {
		respond(w, interlink.CreateStruct{PodUID: j.uid, PodJID: j.id})
		return
	}
	if len(g.jobs) >= maxJobs {
		http.Error(w, "demo job capacity reached; delete old jobs or wait for expiry", http.StatusServiceUnavailable)
		return
	}
	j := &job{uid: p.Metadata.UID, name: p.Metadata.Name, namespace: p.Metadata.Namespace,
		container: p.Spec.Containers[0].Name, id: uuid.NewString(), chunks: chunks,
		counts: make(map[string]int), created: time.Now()}
	g.jobs[j.uid] = j
	g.order = append(g.order, j.uid)
	respond(w, interlink.CreateStruct{PodUID: j.uid, PodJID: j.id})
}

// All scheduler helpers are called with g.mu held.
func (g *gateway) sweep(now time.Time) {
	order := g.order[:0]
	for _, uid := range g.order {
		j := g.jobs[uid]
		if j.finished.IsZero() && now.Sub(j.created) >= jobDuration {
			j.finished, j.reason = now, "DeadlineExceeded"
		}
		if !j.finished.IsZero() && now.Sub(j.finished) >= retention {
			delete(g.jobs, uid)
			continue
		}
		order = append(order, uid)
	}
	g.order = order
}

func (g *gateway) status(w http.ResponseWriter, r *http.Request) {
	var pods []podRef
	if !decode(w, r, &pods) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweep(time.Now())
	statuses := make([]interlink.PodStatus, 0, len(pods))
	for _, p := range pods {
		j := g.jobs[p.Metadata.UID]
		if j == nil {
			continue
		}
		cs := v1.ContainerStatus{Name: j.container, Image: "browser-demo", ImageID: "browser-demo",
			ContainerID: "browser-demo://" + j.id}
		switch {
		case !j.finished.IsZero():
			code := int32(0)
			if j.reason != "Completed" {
				code = 1
			}
			cs.State.Terminated = &v1.ContainerStateTerminated{ExitCode: code, Reason: j.reason,
				StartedAt: metav1.NewTime(j.started), FinishedAt: metav1.NewTime(j.finished)}
		case !j.started.IsZero():
			cs.State.Running = &v1.ContainerStateRunning{StartedAt: metav1.NewTime(j.started)}
		default:
			cs.State.Waiting = &v1.ContainerStateWaiting{Reason: "WaitingForBrowser", Message: "Waiting for opted-in browser workers"}
		}
		statuses = append(statuses, interlink.PodStatus{PodName: j.name, PodUID: j.uid,
			PodNamespace: j.namespace, JobID: j.id, Containers: []v1.ContainerStatus{cs},
			InitContainers: []v1.ContainerStatus{}})
	}
	respond(w, statuses)
}

func (g *gateway) delete(w http.ResponseWriter, r *http.Request) {
	var p podRef
	if !decode(w, r, &p) {
		return
	}
	if p.Metadata.UID == "" {
		http.Error(w, "pod UID required", http.StatusBadRequest)
		return
	}
	g.mu.Lock()
	delete(g.jobs, p.Metadata.UID)
	for i, uid := range g.order {
		if uid == p.Metadata.UID {
			g.order = append(g.order[:i], g.order[i+1:]...)
			break
		}
	}
	g.mu.Unlock()
	respond(w, "deleted")
}

func (g *gateway) logs(w http.ResponseWriter, r *http.Request) {
	var request interlink.LogStruct
	if !decode(w, r, &request) {
		return
	}
	if request.Opts.Follow || request.Opts.Previous || request.Opts.Timestamps ||
		request.Opts.SinceSeconds != 0 || !request.Opts.SinceTime.IsZero() {
		http.Error(w, "demo supports snapshot logs with Tail or Bytes only", http.StatusBadRequest)
		return
	}
	if request.Opts.Tail < 0 || request.Opts.LimitBytes < 0 {
		http.Error(w, "log limits must be nonnegative", http.StatusBadRequest)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweep(time.Now())
	j := g.jobs[request.PodUID]
	if j == nil || (request.ContainerName != "" && request.ContainerName != j.container) {
		http.Error(w, "job/container not found", http.StatusNotFound)
		return
	}
	done := 0
	for _, c := range j.chunks {
		if c.done {
			done++
		}
	}
	state := j.reason
	if state == "" {
		state = "Queued"
		if !j.started.IsZero() {
			state = "Running"
		}
	}
	lines := []string{fmt.Sprintf("state=%s chunks=%d/%d workers=%d", state, done, len(j.chunks), g.workers)}
	if j.reason == "Completed" {
		keys := make([]string, 0, len(j.counts))
		for key := range j.counts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lines = append(lines, fmt.Sprintf("%s: %d", key, j.counts[key]))
		}
	}
	if request.Opts.Tail > 0 && request.Opts.Tail < len(lines) {
		lines = lines[len(lines)-request.Opts.Tail:]
	}
	text := strings.Join(lines, "\n") + "\n"
	if request.Opts.LimitBytes > 0 && request.Opts.LimitBytes < len(text) {
		text = text[:request.Opts.LimitBytes]
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := io.WriteString(w, text); err != nil {
		log.Print("log write failed")
	}
}

type task struct {
	Type    string `json:"type"`
	Attempt string `json:"attempt"`
	Text    string `json:"text"`
}

type result struct {
	Type    string         `json:"type"`
	Attempt string         `json:"attempt"`
	Counts  map[string]int `json:"counts"`
}

func (g *gateway) assign(worker string, now time.Time) (*job, *chunk) {
	g.sweep(now)
	for _, uid := range g.order {
		j := g.jobs[uid]
		if !j.finished.IsZero() {
			continue
		}
		for _, c := range j.chunks {
			if c.done || c.worker != "" {
				continue
			}
			if c.tries >= maxAttempts {
				j.finished, j.reason = now, "AttemptsExceeded"
				break
			}
			c.tries++
			c.worker, c.attempt, c.deadline = worker, uuid.NewString(), now.Add(leaseDuration)
			if j.started.IsZero() {
				j.started = now
			}
			return j, c
		}
	}
	return nil, nil
}

func (g *gateway) accept(j *job, c *chunk, worker string, res result, now time.Time) bool {
	if g.jobs[j.uid] != j || !j.finished.IsZero() || c.done || c.worker != worker ||
		res.Type != "result" || res.Attempt != c.attempt || !now.Before(c.deadline) {
		return false
	}
	total := 0
	for word, count := range res.Counts {
		if !c.allowed[word] || count <= 0 || count > c.total {
			return false
		}
		total += count
	}
	if total != c.total {
		return false
	}
	for word, count := range res.Counts {
		j.counts[word] += count
	}
	c.done, c.worker = true, ""
	for _, part := range j.chunks {
		if !part.done {
			return true
		}
	}
	j.finished, j.reason = now, "Completed"
	return true
}

func (g *gateway) browser(w http.ResponseWriter, r *http.Request) {
	// A connection is opened only after the Join button; require the explicit
	// consent marker as well. Gorilla's default check rejects cross-origin WS.
	if r.URL.Query().Get("consent") != "yes" {
		http.Error(w, "explicit opt-in required", http.StatusForbidden)
		return
	}
	g.mu.Lock()
	if g.workers >= maxWorkers {
		g.mu.Unlock()
		http.Error(w, "worker capacity reached", http.StatusServiceUnavailable)
		return
	}
	g.workers++
	g.mu.Unlock()
	defer func() { g.mu.Lock(); g.workers--; g.mu.Unlock() }()
	upgrader := websocket.Upgrader{HandshakeTimeout: 5 * time.Second}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(32 * 1024)
	messages := make(chan result)
	closed := make(chan struct{})
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		defer close(closed)
		for {
			var res result
			if err := conn.ReadJSON(&res); err != nil {
				return
			}
			select {
			case messages <- res:
			case <-stop:
				return
			}
		}
	}()
	worker := uuid.NewString()
	var j *job
	var c *chunk
	defer func() {
		g.mu.Lock()
		if c != nil && c.worker == worker {
			c.worker, c.attempt = "", ""
		}
		g.mu.Unlock()
	}()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	idleDeadline := time.Now().Add(5 * time.Minute)
	for {
		select {
		case <-closed:
			return
		case res := <-messages:
			g.mu.Lock()
			ok := c != nil && g.accept(j, c, worker, res, time.Now())
			// A mismatched attempt is a late/duplicate message, never a new result.
			stale := c == nil || res.Attempt != c.attempt
			if ok {
				j, c = nil, nil
			}
			g.mu.Unlock()
			if !ok && !stale {
				return
			}
		case now := <-ticker.C:
			g.mu.Lock()
			g.sweep(now)
			if c != nil && (g.jobs[j.uid] != j || !j.finished.IsZero() || !now.Before(c.deadline)) {
				g.mu.Unlock()
				return // Close/release the worker; the UI terminates its Web Worker.
			}
			var message *task
			if c == nil {
				j, c = g.assign(worker, now)
				if c != nil {
					message = &task{Type: "task", Attempt: c.attempt, Text: c.text}
				}
			}
			g.mu.Unlock()
			if message != nil {
				idleDeadline = now.Add(5 * time.Minute)
				if err := conn.SetWriteDeadline(now.Add(2 * time.Second)); err != nil {
					return
				}
				if err := conn.WriteJSON(message); err != nil {
					return
				}
			} else if c == nil && now.After(idleDeadline) {
				return
			}
		}
	}
}

func (g *gateway) api() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /create", g.create)
	mux.HandleFunc("POST /delete", g.delete)
	mux.HandleFunc("GET /status", g.status)
	mux.HandleFunc("GET /getLogs", g.logs)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { respond(w, "ok") })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "" {
			http.Error(w, "plugin API is server-to-server only", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (g *gateway) public() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", g.browser)
	web, err := fs.Sub(assets, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(web)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; worker-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
}

func main() {
	apiAddr := flag.String("api", "127.0.0.1:4000", "private plugin API listen address (never publicly expose)")
	webAddr := flag.String("web", "127.0.0.1:8080", "participant HTTP/WS listen address")
	flag.Parse()
	g := newGateway()
	serve := func(addr string, handler http.Handler) error {
		server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second,
			MaxHeaderBytes: 16 * 1024}
		return server.ListenAndServe()
	}
	go func() { log.Fatal(serve(*apiAddr, g.api())) }()
	log.Printf("private API: %s; participant page: %s", *apiAddr, *webAddr)
	log.Fatal(serve(*webAddr, g.public()))
}
