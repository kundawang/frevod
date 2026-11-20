package dispatcher

import (
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Matheus-Rossetti/frevod/internal/core"
)

// fakeProvider is a fake storage endpoint: it records every job it
// received and can be told to fail, simulating a partial upload failure.
type fakeProvider struct {
	name string
	err  error

	mu   sync.Mutex
	jobs []core.DispatchJob
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Dispatch(_ context.Context, job core.DispatchJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs = append(f.jobs, job)
	return f.err
}

func (f *fakeProvider) receivedJobs() []core.DispatchJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.DispatchJob{}, f.jobs...)
}

func newTestOptions(localEnabled bool) *core.Options {
	options := core.NewOptions()
	options.Encode.ConcurrentEncodings = 1
	options.Dispatch.Local.Enabled = localEnabled
	return options
}

func runDispatcher(t *testing.T, options *core.Options, providers []StorageProvider, jobs ...*core.Job) {
	t.Helper()

	dispatchQueue := make(chan *core.Job, len(jobs))
	log, slog := log.New(io.Discard, "", 0), slog.New(slog.NewTextHandler(io.Discard, nil))
	d := NewDispatcher(context.Background(), log, slog, options, dispatchQueue, providers)

	done := make(chan struct{})
	go func() {
		d.Start()
		close(done)
	}()

	for _, job := range jobs {
		dispatchQueue <- job
	}
	close(dispatchQueue)
	<-done
}

func TestDispatcherDeliversJobToEveryProvider(t *testing.T) {
	fromDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(fromDir, "master.m3u8"), []byte("#EXTM3U"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := &fakeProvider{name: "first"}
	second := &fakeProvider{name: "second"}

	job := core.NewJob()
	job.DispatchJob.FromDir = fromDir
	job.DispatchJob.KeyStarter = "test/video"

	// local disabled: the dispatcher owns the output dir cleanup
	runDispatcher(t, newTestOptions(false), []StorageProvider{first, second}, job)

	for _, provider := range []*fakeProvider{first, second} {
		received := provider.receivedJobs()
		if len(received) != 1 {
			t.Fatalf("provider %v expected 1 job, got %v", provider.name, len(received))
		}
		if received[0].FromDir != fromDir || received[0].KeyStarter != "test/video" {
			t.Errorf("provider %v received the wrong job: %+v", provider.name, received[0])
		}
	}

	if _, err := os.Stat(fromDir); !os.IsNotExist(err) {
		t.Errorf("expected the output dir to be removed after dispatch, stat err: %v", err)
	}
}

func TestDispatcherPartialFailureStillReachesOtherProvidersAndCleansUp(t *testing.T) {
	fromDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(fromDir, "master.m3u8"), []byte("#EXTM3U"), 0o644); err != nil {
		t.Fatal(err)
	}

	failing := &fakeProvider{name: "failing", err: errors.New("upload failed halfway")}
	healthy := &fakeProvider{name: "healthy"}

	job := core.NewJob()
	job.DispatchJob.FromDir = fromDir
	job.DispatchJob.KeyStarter = "test/video"

	runDispatcher(t, newTestOptions(false), []StorageProvider{failing, healthy}, job)

	// a provider failing halfway must not stop the remaining providers
	if got := len(failing.receivedJobs()); got != 1 {
		t.Errorf("expected the failing provider to be called once, got %v", got)
	}
	if got := len(healthy.receivedJobs()); got != 1 {
		t.Errorf("expected the healthy provider to still receive the job, got %v calls", got)
	}

	// partial failure must not leave ghost files behind
	if _, err := os.Stat(fromDir); !os.IsNotExist(err) {
		t.Errorf("expected the output dir to be removed after a partial failure, stat err: %v", err)
	}
}

func TestDispatcherKeepsOutputDirWhenLocalIsEnabled(t *testing.T) {
	fromDir := t.TempDir()

	provider := &fakeProvider{name: "remote"}

	job := core.NewJob()
	job.DispatchJob.FromDir = fromDir
	job.DispatchJob.KeyStarter = "test/video"

	// local renames the dir itself, so the dispatcher must not remove it
	runDispatcher(t, newTestOptions(true), []StorageProvider{provider}, job)

	if _, err := os.Stat(fromDir); err != nil {
		t.Errorf("expected the output dir to be kept when local dispatch is enabled: %v", err)
	}
}
