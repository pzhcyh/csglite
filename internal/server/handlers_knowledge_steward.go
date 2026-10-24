package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/opencsgs/csglite/internal/knowledge"
)

func (s *Server) registerKnowledgeStewardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/knowledge/libraries/{library}/steward", s.knowledgeOnly(s.handleKnowledgeSteward))
	mux.HandleFunc("POST /api/knowledge/libraries/{library}/steward/jobs", s.knowledgeOnly(s.handleKnowledgeStewardQueue))
	mux.HandleFunc("POST /api/knowledge/libraries/{library}/steward/jobs/{job}/run", s.knowledgeOnly(s.handleKnowledgeStewardRun))
	mux.HandleFunc("POST /api/knowledge/libraries/{library}/steward/jobs/{job}/cancel", s.knowledgeOnly(s.handleKnowledgeStewardCancel))
	mux.HandleFunc("POST /api/knowledge/libraries/{library}/steward/jobs/{job}/retry", s.knowledgeOnly(s.handleKnowledgeStewardRetry))
}

func (s *Server) handleKnowledgeSteward(w http.ResponseWriter, r *http.Request) {
	state, err := s.knowledge.Steward(r.PathValue("library"))
	knowledgeResult(w, state, err)
}

func (s *Server) handleKnowledgeStewardQueue(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SourceIDs []string `json:"sourceIds"`
	}
	if !knowledgeDecode(w, r, &input) {
		return
	}
	job, err := s.knowledge.QueueSteward(r.PathValue("library"), input.SourceIDs)
	knowledgeResult(w, job, err)
}

func (s *Server) handleKnowledgeStewardRun(w http.ResponseWriter, r *http.Request) {
	result, err := s.knowledge.RunSteward(r.Context(), r.PathValue("library"), r.PathValue("job"), s.generateStewardProposal)
	knowledgeResult(w, result, err)
}

func (s *Server) handleKnowledgeStewardCancel(w http.ResponseWriter, r *http.Request) {
	job, err := s.knowledge.CancelSteward(r.PathValue("library"), r.PathValue("job"))
	knowledgeResult(w, job, err)
}

func (s *Server) handleKnowledgeStewardRetry(w http.ResponseWriter, r *http.Request) {
	job, err := s.knowledge.RetrySteward(r.PathValue("library"), r.PathValue("job"))
	knowledgeResult(w, job, err)
}

// Reuse the exact native inference handler. In particular, preserve source so
// cloud requests cannot silently load a similarly named local model. This is an
// in-process call: no HTTP roundtrip to an untrusted host or alternate instance.
func (s *Server) generateStewardProposal(ctx context.Context, prompt knowledge.StewardPrompt) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]any{
		"model": prompt.Model, "source": prompt.ModelSource, "stream": true, "temperature": 0.2, "max_tokens": 6000,
		"messages": []map[string]string{{"role": "system", "content": prompt.System}, {"role": "user", "content": prompt.Data}},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	// SSE repeats envelope metadata per token; its wire budget is larger than
	// the final 128 KiB text budget, while still bounded independently of provider.
	recorder := &stewardResponse{header: http.Header{}, limit: 4 * 1024 * 1024, cancel: cancel}
	s.handleOpenAIChatCompletions(recorder, req)
	if recorder.overflow || recorder.status < 200 || recorder.status >= 300 {
		return "", errors.New("selected model request failed")
	}
	// Use the native streaming path so a provider response is bounded while it
	// arrives, before any non-streaming upstream io.ReadAll allocation.
	return parseStewardStream(recorder.body.Bytes())
}

func parseStewardStream(body []byte) (string, error) {
	var response struct {
		Error   json.RawMessage `json:"error"`
		Choices []struct {
			Index int `json:"index"`
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 4096), 2*knowledge.StewardMaxOutputBytes)
	var output strings.Builder
	complete := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			complete = true
			continue
		}
		response.Choices = nil
		response.Error = nil
		if err := json.Unmarshal([]byte(data), &response); err != nil || len(response.Error) > 0 {
			return "", errors.New("selected model returned an invalid event")
		}
		for _, choice := range response.Choices {
			if choice.Index != 0 || choice.FinishReason == "length" || choice.FinishReason == "content_filter" {
				return "", errors.New("selected model returned incomplete output")
			}
			if choice.FinishReason == "stop" {
				complete = true
			}
			if output.Len()+len(choice.Delta.Content) > knowledge.StewardMaxOutputBytes {
				return "", errors.New("selected model exceeded output budget")
			}
			output.WriteString(choice.Delta.Content)
		}
	}
	if scanner.Err() != nil || !complete || output.Len() == 0 {
		return "", errors.New("selected model stream did not complete")
	}
	return output.String(), nil
}

// A model/provider cannot cause an unbounded response allocation. Never return
// its raw error body to the browser: providers may include URLs or credentials.
type stewardResponse struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (w *stewardResponse) Header() http.Header { return w.header }
func (w *stewardResponse) Flush()              {}
func (w *stewardResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *stewardResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.overflow || len(b) > w.limit-w.body.Len() {
		w.overflow = true
		w.cancel()
		return 0, errors.New("model response exceeds steward output budget")
	}
	return w.body.Write(b)
}
