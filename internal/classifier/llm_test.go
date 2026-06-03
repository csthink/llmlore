package classifier

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/csthink/llmlore/internal/model"
)

// stubProvider returns a canned reply or error, recording the prompts it saw.
type stubProvider struct {
	reply     string
	err       error
	gotSystem string
	gotUser   string
}

func (s *stubProvider) Complete(_ context.Context, system, user string) (string, error) {
	s.gotSystem, s.gotUser = system, user
	return s.reply, s.err
}

func TestLLMClassifyParsesReply(t *testing.T) {
	p := &stubProvider{reply: `{"keep": true, "type": "tutorial", "topics": ["llm","agent"], "summary": "Teaches building agents."}`}
	l := NewLLM(p)

	dec, err := l.Classify(context.Background(), cand("course", "desc", "Python"))
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	// The provider must receive the spec-encoded rules and the candidate facts.
	if p.gotSystem != classifySystemPrompt {
		t.Errorf("system prompt was not the classify prompt")
	}
	if !strings.Contains(p.gotUser, "owner/course") {
		t.Errorf("user prompt %q missing the candidate id", p.gotUser)
	}
	if !dec.Keep || dec.Type != model.TypeTutorial {
		t.Errorf("got Keep=%v Type=%q, want true/tutorial", dec.Keep, dec.Type)
	}
	if dec.Summary != "Teaches building agents." {
		t.Errorf("Summary = %q", dec.Summary)
	}
	if dec.ClassifiedBy != model.ClassifiedByLLM {
		t.Errorf("ClassifiedBy = %q, want llm", dec.ClassifiedBy)
	}
	if !contains(dec.Topics, model.TopicLLM) || !contains(dec.Topics, model.TopicAgent) {
		t.Errorf("Topics = %v", dec.Topics)
	}
}

func TestLLMClassifyHandlesFencesAndProse(t *testing.T) {
	p := &stubProvider{reply: "Sure!\n```json\n{\"keep\": true, \"type\": \"guide\", \"topics\": [\"rag\"], \"summary\": \"A RAG guide.\"}\n```"}
	dec, err := NewLLM(p).Classify(context.Background(), cand("x", "d", "Go"))
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if !dec.Keep || dec.Type != model.TypeGuide {
		t.Errorf("got Keep=%v Type=%q, want true/guide", dec.Keep, dec.Type)
	}
}

func TestLLMClassifyCoercesOutOfVocabTopics(t *testing.T) {
	// "blockchain" is out of vocabulary → dropped; "agent" kept.
	p := &stubProvider{reply: `{"keep": true, "type": "example", "topics": ["blockchain","agent"], "summary": "x"}`}
	dec, _ := NewLLM(p).Classify(context.Background(), cand("x", "d", "Go"))
	if contains(dec.Topics, "blockchain") {
		t.Errorf("Topics = %v, must not contain out-of-vocab topic", dec.Topics)
	}
	if !contains(dec.Topics, model.TopicAgent) {
		t.Errorf("Topics = %v, want to keep valid topic agent", dec.Topics)
	}
}

func TestLLMClassifyAllInvalidTopicsBecomeOther(t *testing.T) {
	p := &stubProvider{reply: `{"keep": true, "type": "example", "topics": ["blockchain","crypto"], "summary": "x"}`}
	dec, _ := NewLLM(p).Classify(context.Background(), cand("x", "d", "Go"))
	if len(dec.Topics) != 1 || dec.Topics[0] != model.TopicOther {
		t.Errorf("Topics = %v, want [other]", dec.Topics)
	}
}

func TestLLMClassifyKeepFalse(t *testing.T) {
	p := &stubProvider{reply: `{"keep": false}`}
	dec, err := NewLLM(p).Classify(context.Background(), cand("x", "d", "Go"))
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if dec.Keep {
		t.Errorf("Keep = true, want false")
	}
	if dec.ClassifiedBy != model.ClassifiedByLLM {
		t.Errorf("ClassifiedBy = %q, want llm", dec.ClassifiedBy)
	}
}

func TestLLMClassifyFallsBackToHeuristicOnBadReply(t *testing.T) {
	// Unusable replies (garbage, missing keep, invalid type) must degrade to the
	// heuristic for this candidate, marked classified_by=heuristic.
	c := cand("llm-tutorial", "A tutorial to learn building llm agents", "Python")
	for _, reply := range []string{
		"not json at all",
		`{"type": "tutorial", "topics": ["llm"]}`, // missing keep
		`{"keep": true, "type": "nonsense"}`,      // invalid type
	} {
		p := &stubProvider{reply: reply}
		dec, err := NewLLM(p).Classify(context.Background(), c)
		if err != nil {
			t.Fatalf("Classify(%q): unexpected error %v", reply, err)
		}
		if dec.ClassifiedBy != model.ClassifiedByHeuristic {
			t.Errorf("reply %q: ClassifiedBy = %q, want heuristic fallback", reply, dec.ClassifiedBy)
		}
	}
}

func TestLLMClassifyProviderErrorIsFatal(t *testing.T) {
	// A provider transport/credential error is NOT a per-candidate fallback: it
	// propagates so the CLI can map an exit code.
	wantErr := &MissingConfigError{Op: "x", Err: fmt.Errorf("401")}
	p := &stubProvider{err: wantErr}
	_, err := NewLLM(p).Classify(context.Background(), cand("x", "d", "Go"))
	if !IsMissingConfig(err) {
		t.Fatalf("err = %v, want a propagated MissingConfigError", err)
	}
}

// --- chatProvider (OpenAI-compatible) over httptest -------------------------

func newChatProvider(url string) *chatProvider {
	return &chatProvider{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		baseURL:    url,
		apiKey:     "secret",
		model:      "test-model",
		maxRetries: 2,
		baseDelay:  time.Millisecond,
		sleep:      func(time.Duration) {}, // no real sleeping in tests
	}
}

func TestChatProviderSuccess(t *testing.T) {
	var gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hello"}}]}`)
	}))
	t.Cleanup(srv.Close)

	got, err := newChatProvider(srv.URL).Complete(context.Background(), "sys", "usr")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "hello" {
		t.Errorf("content = %q, want hello", got)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("auth = %q, want Bearer secret", gotAuth)
	}
	if gotModel != "test-model" {
		t.Errorf("model = %q, want test-model", gotModel)
	}
}

func TestChatProviderUnauthorizedIsMissingConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"invalid api key"}}`)
	}))
	t.Cleanup(srv.Close)

	_, err := newChatProvider(srv.URL).Complete(context.Background(), "sys", "usr")
	if !IsMissingConfig(err) {
		t.Fatalf("err = %v, want MissingConfigError (exit %d)", err, ExitMissingConfig)
	}
	if IsUpstream(err) {
		t.Errorf("401 must not be classified as upstream/exit 3: %v", err)
	}
	// The uncontrolled upstream text must be present but source-labeled, so it
	// can never read as llmlore's own copy (AC-9 edge safety).
	msg := err.Error()
	if !strings.Contains(msg, "upstream provider message:") {
		t.Errorf("error %q does not label the passthrough source", msg)
	}
	if !strings.Contains(msg, "invalid api key") {
		t.Errorf("error %q dropped the upstream message", msg)
	}
}

func TestLabeledProviderMessageHandlesEmptyBody(t *testing.T) {
	if got := labeledProviderMessage(nil); strings.Contains(got, "\"\"") {
		t.Errorf("empty body produced an empty quoted passthrough: %q", got)
	}
}

func TestChatProviderRetriesThenSucceeds(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusTooManyRequests) // 429 twice
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	t.Cleanup(srv.Close)

	got, err := newChatProvider(srv.URL).Complete(context.Background(), "sys", "usr")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "ok" {
		t.Errorf("content = %q, want ok", got)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (two 429 retries then success)", calls)
	}
}

func TestChatProviderRetriesExhaustedIsUpstream(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError) // always 500
	}))
	t.Cleanup(srv.Close)

	_, err := newChatProvider(srv.URL).Complete(context.Background(), "sys", "usr")
	if !IsUpstream(err) {
		t.Fatalf("err = %v, want UpstreamError (exit %d) after retries", err, ExitUpstream)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (initial + 2 retries)", calls)
	}
}
