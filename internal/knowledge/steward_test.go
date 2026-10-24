package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func stewardFixture(t *testing.T) (*Store, *Library, string) {
	t.Helper()
	s, lib, root := fixture(t)
	lib, err := s.Configure(lib.ID, Config{Model: "fictional-model", ModelSource: "cloud"})
	if err != nil {
		t.Fatal(err)
	}
	return s, lib, root
}

func stewardOutput(source string) string {
	b, _ := json.Marshal(map[string]any{"changes": []map[string]string{{"pageId": "", "title": "虚构整理结果", "content": "虚构资料整理，仍待审阅。[source:" + source + "]"}}})
	return string(b)
}

func TestStewardQueueRunReviewAndPersistence(t *testing.T) {
	s, lib, root := stewardFixture(t)
	job, err := s.QueueSteward(lib.ID, []string{lib.Sources[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	s, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.Steward(lib.ID)
	if err != nil || len(state.Jobs) != 1 || state.Jobs[0].Status != "queued" {
		t.Fatalf("queue not persistent: %+v %v", state, err)
	}
	result, err := s.RunSteward(context.Background(), lib.ID, job.ID, func(ctx context.Context, p StewardPrompt) (string, error) {
		if p.Model != "fictional-model" || p.ModelSource != "cloud" || !strings.Contains(p.Data, lib.Sources[0].SHA256) {
			t.Fatal("model identity/source snapshot lost")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("no bounded deadline")
		}
		return stewardOutput(lib.Sources[0].ID), nil
	})
	if err != nil || result.Job.Status != "completed" || result.ProposalID == "" {
		t.Fatalf("run: %+v %v", result, err)
	}
	got, _ := s.Get(lib.ID)
	if len(got.Pages) != len(lib.Pages) || got.Proposals[len(got.Proposals)-1].Status != "pending" {
		t.Fatal("model published directly")
	}
	_, err = s.RunSteward(context.Background(), lib.ID, job.ID, func(context.Context, StewardPrompt) (string, error) {
		t.Fatal("duplicate run called model")
		return "", nil
	})
	requireStatus(t, err, 409)
	got, err = s.Review(lib.ID, result.ProposalID, true)
	if err != nil || len(got.Pages) != len(lib.Pages)+1 {
		t.Fatalf("review: %v", err)
	}
	s, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	state, _ = s.Steward(lib.ID)
	if state.Jobs[0].ProposalID != result.ProposalID {
		t.Fatal("job/proposal association lost")
	}
}

func TestStewardRejectsChangedSnapshot(t *testing.T) {
	for _, change := range []string{"withdraw", "config", "page"} {
		t.Run(change, func(t *testing.T) {
			s, lib, _ := stewardFixture(t)
			job, err := s.QueueSteward(lib.ID, []string{lib.Sources[0].ID})
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.RunSteward(context.Background(), lib.ID, job.ID, func(context.Context, StewardPrompt) (string, error) {
				var updateErr error
				switch change {
				case "withdraw":
					_, updateErr = s.Withdraw(lib.ID, lib.Sources[0].ID)
				case "config":
					_, updateErr = s.Configure(lib.ID, Config{Model: "different-model", ModelSource: "local"})
				case "page":
					for _, page := range lib.Pages {
						if _, ok := job.PageVersions[page.ID]; ok {
							_, updateErr = s.SavePage(lib.ID, page.ID, PageInput{Title: page.Title, Content: page.Content + "\n人工更新", SourceIDs: page.SourceIDs, ExpectedVersion: page.Version})
							break
						}
					}
				}
				if updateErr != nil {
					t.Fatal(updateErr)
				}
				return stewardOutput(lib.Sources[0].ID), nil
			})
			if err != nil || result.Job.Status != "failed" || result.ProposalID != "" {
				t.Fatalf("stale output accepted: %+v %v", result, err)
			}
			got, _ := s.Get(lib.ID)
			if len(got.Proposals) != len(lib.Proposals) {
				t.Fatal("stale proposal persisted")
			}
		})
	}
}

func TestStewardCancelPreventsLateProposalAndDuplicateRun(t *testing.T) {
	s, lib, _ := stewardFixture(t)
	job, err := s.QueueSteward(lib.ID, []string{lib.Sources[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	type outcome struct {
		result *StewardResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := s.RunSteward(context.Background(), lib.ID, job.ID, func(ctx context.Context, _ StewardPrompt) (string, error) {
			close(started)
			<-release
			if ctx.Err() == nil {
				t.Error("cancel context not signaled")
			}
			return stewardOutput(lib.Sources[0].ID), nil
		})
		done <- outcome{result, err}
	}()
	<-started
	_, err = s.RunSteward(context.Background(), lib.ID, job.ID, func(context.Context, StewardPrompt) (string, error) { t.Error("duplicate invocation"); return "", nil })
	requireStatus(t, err, 409)
	cancelled, err := s.CancelSteward(lib.ID, job.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancel %v", err)
	}
	_, err = s.RetrySteward(lib.ID, job.ID)
	requireStatus(t, err, 409)
	other, err := s.QueueSteward(lib.ID, []string{lib.Sources[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RunSteward(context.Background(), lib.ID, other.ID, func(context.Context, StewardPrompt) (string, error) {
		t.Error("new run overlapped stopping run")
		return "", nil
	})
	requireStatus(t, err, 409)
	close(release)
	got := <-done
	if got.err != nil || got.result.Job.Status != "cancelled" {
		t.Fatalf("late result: %+v", got)
	}
	current, _ := s.Get(lib.ID)
	if len(current.Proposals) != len(lib.Proposals) {
		t.Fatal("cancelled request wrote proposal")
	}
	retry, err := s.RetrySteward(lib.ID, job.ID)
	if err != nil || retry.Status != "queued" {
		t.Fatalf("retry: %v", err)
	}
}

func TestStewardPreflightChangeFailsWithoutCallingModel(t *testing.T) {
	s, lib, _ := stewardFixture(t)
	job, err := s.QueueSteward(lib.ID, []string{lib.Sources[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Configure(lib.ID, Config{Model: "updated-model", ModelSource: "local"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.RunSteward(context.Background(), lib.ID, job.ID, func(context.Context, StewardPrompt) (string, error) {
		t.Fatal("stale config called model")
		return "", nil
	})
	if err != nil || result.Job.Status != "failed" || result.Job.Attempts != 0 {
		t.Fatalf("preflight %+v %v", result, err)
	}
	fresh, err := s.RetrySteward(lib.ID, job.ID)
	if err != nil || fresh.Model != "updated-model" || fresh.ModelSource != "local" {
		t.Fatalf("retry failed to refresh model %+v %v", fresh, err)
	}
}

func TestStewardRetryBudgetAndSanitizedErrors(t *testing.T) {
	s, lib, _ := stewardFixture(t)
	job, err := s.QueueSteward(lib.ID, []string{lib.Sources[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		result, err := s.RunSteward(context.Background(), lib.ID, job.ID, func(context.Context, StewardPrompt) (string, error) {
			return "", errors.New("Authorization Bearer secret /private/home/path")
		})
		if err != nil || result.Job.Attempts != i || result.Job.Status != "failed" || strings.Contains(result.Job.Error, "secret") {
			t.Fatalf("failure not safe/bounded: %+v %v", result, err)
		}
		_, err = s.RetrySteward(lib.ID, job.ID)
		if i == 3 {
			requireStatus(t, err, 409)
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func TestStewardRejectsInvalidModelOutput(t *testing.T) {
	for _, output := range []string{"not json", `{"changes":[]}`, `{"changes":[{"pageId":"","title":"X","content":"no citation"}]}`, stewardOutput("src-foreign"), stewardOutput("PLACEHOLDER") + " {}", strings.Repeat("x", StewardMaxOutputBytes+1)} {
		t.Run(output[:min(len(output), 24)], func(t *testing.T) {
			s, lib, _ := stewardFixture(t)
			job, err := s.QueueSteward(lib.ID, []string{lib.Sources[0].ID})
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.RunSteward(context.Background(), lib.ID, job.ID, func(context.Context, StewardPrompt) (string, error) {
				return strings.ReplaceAll(output, "PLACEHOLDER", lib.Sources[0].ID), nil
			})
			if err != nil || result.Job.Status != "failed" || result.ProposalID != "" {
				t.Fatalf("bad output accepted: %+v %v", result, err)
			}
		})
	}
}

func TestStewardRestartAndRestoreDoNotAutostart(t *testing.T) {
	s, lib, root := stewardFixture(t)
	job, err := s.QueueSteward(lib.ID, []string{lib.Sources[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.mutate(lib.ID, func(lib *Library) error { findStewardJob(lib, job.ID).Status = "running"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	s, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.Steward(lib.ID)
	if err != nil || state.Jobs[0].Status != "failed" || !strings.Contains(state.Jobs[0].Error, "中断") {
		t.Fatalf("restart: %+v %v", state, err)
	}
	_, err = s.RetrySteward(lib.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	lib, _ = s.Get(lib.ID)
	NormalizeRestoredSteward(lib)
	if lib.StewardJobs[0].Status != "cancelled" {
		t.Fatal("restored job could execute")
	}
}

func TestStewardQueueBoundsAndHealth(t *testing.T) {
	s, lib, _ := stewardFixture(t)
	_, err := s.QueueSteward(lib.ID, nil)
	requireStatus(t, err, 400)
	_, err = s.QueueSteward(lib.ID, []string{lib.Sources[0].ID, lib.Sources[0].ID})
	requireStatus(t, err, 400)
	lib, err = s.AddSource(lib.ID, SourceInput{Title: "虚构重复资料", Filename: "duplicate.md", Content: lib.Sources[0].Content})
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.Steward(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, uncompiled := false, false
	for _, issue := range state.Issues {
		duplicate = duplicate || issue.Kind == "duplicate_source"
		uncompiled = uncompiled || issue.Kind == "uncompiled_source"
	}
	if !duplicate || !uncompiled {
		t.Fatalf("health %+v", state.Issues)
	}
	lib, err = s.AddSource(lib.ID, SourceInput{Title: "虚构大资料", Filename: "big.txt", Content: strings.Repeat("x", StewardMaxInputBytes+1)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.QueueSteward(lib.ID, []string{lib.Sources[len(lib.Sources)-1].ID})
	requireStatus(t, err, 400)
	for i := 0; i < 20; i++ {
		if _, err = s.QueueSteward(lib.ID, []string{lib.Sources[0].ID}); err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.QueueSteward(lib.ID, []string{lib.Sources[0].ID})
	requireStatus(t, err, 409)
	_, err = s.Withdraw(lib.ID, lib.Sources[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	state, _ = s.Steward(lib.ID)
	missing := false
	for _, issue := range state.Issues {
		missing = missing || issue.Kind == "missing_source"
	}
	if !missing {
		t.Fatal("withdrawn source not detected")
	}
}
