package encoder

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Matheus-Rossetti/frevod/internal/core"
)

// ---------- TEST HELPERS ----------

type stream struct {
	codecType string
	width     int
	height    int
}

func metadataWithStreams(streams ...stream) *Metadata {
	type jsonStream struct {
		CodecType string `json:"codec_type"`
		Width     int    `json:"width,omitempty"`
		Height    int    `json:"height,omitempty"`
	}
	jsonStreams := make([]jsonStream, 0, len(streams))
	for _, s := range streams {
		jsonStreams = append(jsonStreams, jsonStream{CodecType: s.codecType, Width: s.width, Height: s.height})
	}
	raw, _ := json.Marshal(map[string]any{"streams": jsonStreams})

	var metadata Metadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		panic(err)
	}
	return &metadata
}

// fakeFFmpeg creates a directory with fake `ffmpeg` and `ffprobe`
// binaries and prepends it to PATH for the duration of the test.
//
// The fake ffprobe prints the JSON file pointed to by FFPROBE_FAKE_JSON.
// The fake ffmpeg records every invocation as a separate file inside the
// returned dir (one arg per line, so multi-word args survive intact and
// concurrent invocations never interleave), then materializes a fake HLS
// output (variant playlists, stream dirs and the master playlist) derived
// from the -var_stream_map and -master_pl_name args, exactly where the
// real FFmpeg would put them. If FFMPEG_FAKE_SLEEP is set to a non-zero
// number of seconds, it sleeps instead of producing output.
func fakeFFmpeg(t *testing.T) (argsDir string) {
	t.Helper()

	binDir := t.TempDir()
	argsDir = t.TempDir()

	ffprobe := `#!/bin/sh
cat "$FFPROBE_FAKE_JSON"
`

	ffmpeg := `#!/bin/bash
set -euo pipefail
inv_file="` + argsDir + `/$(date +%s%N)_$$.txt"
for a in "$@"; do echo "$a"; done > "$inv_file"

if [ "${FFMPEG_FAKE_SLEEP:-0}" != "0" ]; then
  sleep "$FFMPEG_FAKE_SLEEP"
  exit 0
fi

master="master.m3u8"
varmap=""
playlist=""
prev=""
for a in "$@"; do
  case "$prev" in
    -master_pl_name) master="$a" ;;
    -var_stream_map) varmap="$a" ;;
  esac
  prev="$a"
done
for a in "$@"; do playlist="$a"; done

i=0
playlists=()
for variant in $varmap; do
  dir="stream_$i"
  mkdir -p "$dir"
  echo init > "$dir/init.mp4"
  echo seg  > "$dir/segment_000.m4s"
  pl=$(echo "$playlist" | sed "s/%v/$i/")
  printf '#EXTM3U\n#EXTINF:2.0,\n%s/segment_000.m4s\n#EXT-X-ENDLIST\n' "$dir" > "$pl"
  playlists+=("$pl")
  i=$((i+1))
done

{
  echo "#EXTM3U"
  for pl in "${playlists[@]}"; do
    echo "#EXT-X-STREAM-INF:BANDWIDTH=1000000"
    echo "$pl"
  done
} > "$master"
`

	writeExecutable(t, filepath.Join(binDir, "ffprobe"), ffprobe)
	writeExecutable(t, filepath.Join(binDir, "ffmpeg"), ffmpeg)

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FFMPEG_FAKE_SLEEP", "0")
	return argsDir
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("failed to write fake binary %v: %v", path, err)
	}
}

func writeFakeMetadata(t *testing.T, metadata *Metadata) {
	t.Helper()
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("failed to marshal fake metadata: %v", err)
	}
	jsonPath := filepath.Join(t.TempDir(), "metadata.json")
	if err := os.WriteFile(jsonPath, raw, 0o644); err != nil {
		t.Fatalf("failed to write fake metadata: %v", err)
	}
	t.Setenv("FFPROBE_FAKE_JSON", jsonPath)
}

// chdir switches the test to a fresh temp dir, because the encoder
// creates its "output/" dir relative to the working directory.
func chdir(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working dir: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(oldWd) })
	return tmp
}

func discardLoggers() (*log.Logger, *slog.Logger) {
	return log.New(io.Discard, "", 0), slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestOptions() *core.Options {
	options := core.NewOptions()
	options.Encode.ConcurrentEncodings = 2
	return options
}

// testRig wires fresh queues to one or more encoders and runs them
// until the encode queue is closed.
//
// NOTE: closing the encode queue makes the encoders delete the whole
// "output/" dir, so tests must assert on the dispatched output BEFORE
// calling finish.
type testRig struct {
	encodeQueue   chan *core.Job
	dispatchQueue chan *core.Job

	done chan struct{}
	once sync.Once
}

func newTestRig(ctx context.Context, t *testing.T, options *core.Options, filePool chan *os.File, encoderCount int) *testRig {
	t.Helper()

	rig := &testRig{
		encodeQueue:   make(chan *core.Job, 8),
		dispatchQueue: make(chan *core.Job, 8),
		done:          make(chan struct{}),
	}

	var wg sync.WaitGroup
	for range encoderCount {
		log, slog := discardLoggers()
		encoder := NewEncoder(ctx, log, slog, options, filePool, rig.encodeQueue, rig.dispatchQueue)
		wg.Go(func() { encoder.Start() })
	}
	go func() {
		wg.Wait()
		close(rig.done)
	}()

	return rig
}

// finish closes the encode queue and waits for the encoders to stop.
// It is idempotent so tests can also call it from a cleanup.
func (r *testRig) finish() {
	r.once.Do(func() {
		close(r.encodeQueue)
		<-r.done
	})
}

func (r *testRig) waitDispatchedJob(t *testing.T) *core.Job {
	t.Helper()
	select {
	case job := <-r.dispatchQueue:
		return job
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for a job on the dispatch queue")
		return nil
	}
}

func newTestJob(t *testing.T, videoPath string, ladder core.Ladder) *core.Job {
	t.Helper()
	file, err := os.Open(videoPath)
	if err != nil {
		t.Fatalf("failed to open test video: %v", err)
	}
	t.Cleanup(func() { file.Close() })

	job := core.NewJob()
	job.DispatchJob.KeyStarter = "test/video"
	job.EncodeJob.File = file
	job.EncodeJob.DownloadedFile = false
	job.EncodeJob.Ladder = ladder
	return job
}

func writeTestVideo(t *testing.T) string {
	t.Helper()
	videoPath := filepath.Join(t.TempDir(), "video.mp4")
	if err := os.WriteFile(videoPath, []byte("fake video"), 0o644); err != nil {
		t.Fatal(err)
	}
	return videoPath
}

// readInvocations parses the fake ffmpeg args dir: one entry per
// invocation, each a slice of the exact args it received, ordered by
// invocation time.
func readInvocations(t *testing.T, argsDir string) [][]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(argsDir, "*.txt"))
	if err != nil {
		t.Fatalf("failed to glob ffmpeg args dir: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("fake ffmpeg was never invoked")
	}
	slices.Sort(files) // filenames start with a nanosecond timestamp

	invocations := make([][]string, 0, len(files))
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("failed to read ffmpeg args file: %v", err)
		}
		invocations = append(invocations, strings.Split(strings.TrimSpace(string(raw)), "\n"))
	}
	return invocations
}

// flagValue returns the value right after the first occurrence of flag.
func flagValue(args []string, flag string) string {
	for index, arg := range args {
		if arg == flag && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func assertHLSOutput(t *testing.T, fromDir string, variantCount int) {
	t.Helper()

	expected := []string{"master.m3u8"}
	for index := range variantCount {
		expected = append(expected,
			"playlist_"+strconv.Itoa(index)+".m3u8",
			"stream_"+strconv.Itoa(index)+"/init.mp4",
		)
	}
	for _, file := range expected {
		if _, err := os.Stat(filepath.Join(fromDir, file)); err != nil {
			t.Errorf("expected HLS output %v to exist: %v", file, err)
		}
	}

	master, err := os.ReadFile(filepath.Join(fromDir, "master.m3u8"))
	if err != nil {
		t.Fatalf("failed to read master playlist: %v", err)
	}
	for index := range variantCount {
		playlist := "playlist_" + strconv.Itoa(index) + ".m3u8"
		if !strings.Contains(string(master), playlist) {
			t.Errorf("expected master playlist to reference %v, got:\n%v", playlist, master)
		}
	}
}

// ---------- TESTS ----------

func TestEncoderCustomLadderOnVerticalVideo(t *testing.T) {
	chdir(t)
	argsDir := fakeFFmpeg(t)
	writeFakeMetadata(t, metadataWithStreams(
		stream{codecType: "video", width: 1080, height: 1920},
		stream{codecType: "audio"},
	))

	// a vertical short doesn't need the five horizontal rungs:
	// a compact 3-rung ladder, given out of order on purpose
	ladder := core.Ladder{
		{Name: "480", Resolution: 480, VideoBitrate: "1200k", MaxRate: "1600k", BufSize: "2400k", AudioBitrate: "96k"},
		{Name: "1080", Resolution: 1080, VideoBitrate: "4500k", MaxRate: "6000k", BufSize: "9000k", AudioBitrate: "160k"},
		{Name: "720", Resolution: 720, VideoBitrate: "2500k", MaxRate: "3500k", BufSize: "5000k", AudioBitrate: "128k"},
	}

	rig := newTestRig(context.Background(), t, newTestOptions(), make(chan *os.File, 1), 1)

	rig.encodeQueue <- newTestJob(t, writeTestVideo(t), ladder)
	dispatched := rig.waitDispatchedJob(t)

	// --- the FFmpeg command used exactly the custom rungs, normalized ---
	invocations := readInvocations(t, argsDir)
	if len(invocations) != 1 {
		t.Fatalf("expected exactly one ffmpeg invocation, got %v", len(invocations))
	}
	args := invocations[0]

	filterComplex := flagValue(args, "-filter_complex")
	for _, expected := range []string{
		"split=3",
		"scale=1080:-2", // vertical videos scale by width
		"scale=720:-2",
		"scale=480:-2",
	} {
		if !strings.Contains(filterComplex, expected) {
			t.Errorf("expected filter_complex to contain %q, got %q", expected, filterComplex)
		}
	}

	expectedBitrates := map[string]string{
		"-b:v:0": "4500k", "-maxrate:v:0": "6000k", "-bufsize:v:0": "9000k",
		"-b:v:1": "2500k", "-maxrate:v:1": "3500k", "-bufsize:v:1": "5000k",
		"-b:v:2": "1200k", "-maxrate:v:2": "1600k", "-bufsize:v:2": "2400k",
		"-b:a:0": "160k", "-b:a:1": "128k", "-b:a:2": "96k",
	}
	for flag, expected := range expectedBitrates {
		if got := flagValue(args, flag); got != expected {
			t.Errorf("expected %v to be %v, got %v", flag, expected, got)
		}
	}

	if got := flagValue(args, "-var_stream_map"); got != "v:0,a:0 v:1,a:1 v:2,a:2 " {
		t.Errorf("unexpected var_stream_map: %q", got)
	}

	// --- the HLS output holds one variant per rung plus the master playlist ---
	assertHLSOutput(t, dispatched.DispatchJob.FromDir, 3)

	// --- the ladder was pinned to the job, so a retry reuses these rungs ---
	if len(dispatched.EncodeJob.Ladder) != 3 {
		t.Errorf("expected the resolved ladder to be pinned to the job, got %v", dispatched.EncodeJob.Ladder)
	}

	rig.finish()
}

func TestEncoderDefaultLadderWhenJobAndConfigHaveNone(t *testing.T) {
	chdir(t)
	argsDir := fakeFFmpeg(t)
	writeFakeMetadata(t, metadataWithStreams(
		stream{codecType: "video", width: 1920, height: 1080},
		stream{codecType: "audio"},
	))

	rig := newTestRig(context.Background(), t, newTestOptions(), make(chan *os.File, 1), 1)

	rig.encodeQueue <- newTestJob(t, writeTestVideo(t), nil) // no custom ladder
	dispatched := rig.waitDispatchedJob(t)

	args := readInvocations(t, argsDir)[0]

	filterComplex := flagValue(args, "-filter_complex")
	for _, expected := range []string{"split=3", "scale=-2:1080", "scale=-2:720", "scale=-2:480"} {
		if !strings.Contains(filterComplex, expected) {
			t.Errorf("expected filter_complex to contain %q, got %q", expected, filterComplex)
		}
	}
	if strings.Contains(filterComplex, "2160") || strings.Contains(filterComplex, "1440") {
		t.Errorf("rungs above the source resolution should have been filtered out: %q", filterComplex)
	}

	// the original hardcoded bitrates must be preserved by the default ladder
	expectedBitrates := map[string]string{
		"-b:v:0": "6000k", "-maxrate:v:0": "8000k", "-bufsize:v:0": "12000k",
		"-b:v:1": "3500k", "-maxrate:v:1": "5000k", "-bufsize:v:1": "7000k",
		"-b:v:2": "1500k", "-maxrate:v:2": "2000k", "-bufsize:v:2": "3000k",
	}
	for flag, expected := range expectedBitrates {
		if got := flagValue(args, flag); got != expected {
			t.Errorf("expected %v to be %v, got %v", flag, expected, got)
		}
	}

	assertHLSOutput(t, dispatched.DispatchJob.FromDir, 3)
	rig.finish()
}

func TestEncoderConfigLadderIsUsedWhenJobHasNone(t *testing.T) {
	chdir(t)
	argsDir := fakeFFmpeg(t)
	writeFakeMetadata(t, metadataWithStreams(
		stream{codecType: "video", width: 1920, height: 1080},
		stream{codecType: "audio"},
	))

	options := newTestOptions()
	options.Encode.Ladder = core.Ladder{
		{Name: "1080", Resolution: 1080, VideoBitrate: "5000k", MaxRate: "7000k", BufSize: "10000k", AudioBitrate: "192k"},
		{Name: "360", Resolution: 360, VideoBitrate: "800k", MaxRate: "1000k", BufSize: "1600k", AudioBitrate: "96k"},
	}

	rig := newTestRig(context.Background(), t, options, make(chan *os.File, 1), 1)

	rig.encodeQueue <- newTestJob(t, writeTestVideo(t), nil)
	rig.waitDispatchedJob(t)

	args := readInvocations(t, argsDir)[0]
	filterComplex := flagValue(args, "-filter_complex")
	for _, expected := range []string{"split=2", "scale=-2:1080", "scale=-2:360"} {
		if !strings.Contains(filterComplex, expected) {
			t.Errorf("expected filter_complex to contain %q, got %q", expected, filterComplex)
		}
	}
	if got := flagValue(args, "-b:v:1"); got != "800k" {
		t.Errorf("expected config ladder bitrate 800k, got %v", got)
	}

	rig.finish()
}

func TestEncoderNoAudioTrack(t *testing.T) {
	chdir(t)
	argsDir := fakeFFmpeg(t)
	writeFakeMetadata(t, metadataWithStreams(
		stream{codecType: "video", width: 1280, height: 720},
	))

	rig := newTestRig(context.Background(), t, newTestOptions(), make(chan *os.File, 1), 1)

	rig.encodeQueue <- newTestJob(t, writeTestVideo(t), nil)
	dispatched := rig.waitDispatchedJob(t)

	args := readInvocations(t, argsDir)[0]

	for _, arg := range args {
		if arg == "a:0" || strings.HasPrefix(arg, "-c:a") || strings.HasPrefix(arg, "-b:a") {
			t.Errorf("expected no audio flags for a video without audio, got arg %q", arg)
		}
	}
	if got := flagValue(args, "-var_stream_map"); got != "v:0 v:1 " {
		t.Errorf("expected video-only var_stream_map, got %q", got)
	}

	// the master playlist is still generated for the video-only variants
	assertHLSOutput(t, dispatched.DispatchJob.FromDir, 2)
	rig.finish()
}

func TestEncoderAllRungsFilteredFallsBackToLowest(t *testing.T) {
	chdir(t)
	argsDir := fakeFFmpeg(t)
	writeFakeMetadata(t, metadataWithStreams(
		stream{codecType: "video", width: 360, height: 640},
		stream{codecType: "audio"},
	))

	ladder := core.Ladder{
		{Name: "1080", Resolution: 1080, VideoBitrate: "4500k", MaxRate: "6000k", BufSize: "9000k", AudioBitrate: "160k"},
		{Name: "720", Resolution: 720, VideoBitrate: "2500k", MaxRate: "3500k", BufSize: "5000k", AudioBitrate: "128k"},
	}

	rig := newTestRig(context.Background(), t, newTestOptions(), make(chan *os.File, 1), 1)

	rig.encodeQueue <- newTestJob(t, writeTestVideo(t), ladder)
	dispatched := rig.waitDispatchedJob(t)

	args := readInvocations(t, argsDir)[0]
	filterComplex := flagValue(args, "-filter_complex")
	if !strings.Contains(filterComplex, "split=1") || !strings.Contains(filterComplex, "scale=720:-2") {
		t.Errorf("expected a single 720 fallback rung, got %q", filterComplex)
	}
	if got := flagValue(args, "-var_stream_map"); got != "v:0,a:0 " {
		t.Errorf("expected a single variant in var_stream_map, got %q", got)
	}

	assertHLSOutput(t, dispatched.DispatchJob.FromDir, 1)
	rig.finish()
}

func TestEncoderConcurrentJobsKeepTheirOwnLadders(t *testing.T) {
	chdir(t)
	argsDir := fakeFFmpeg(t)

	// both jobs share the same source metadata but carry different ladders
	writeFakeMetadata(t, metadataWithStreams(
		stream{codecType: "video", width: 1920, height: 1080},
		stream{codecType: "audio"},
	))

	bigLadder := core.Ladder{
		{Name: "1080", Resolution: 1080, VideoBitrate: "6000k", MaxRate: "8000k", BufSize: "12000k", AudioBitrate: "192k"},
		{Name: "720", Resolution: 720, VideoBitrate: "3500k", MaxRate: "5000k", BufSize: "7000k", AudioBitrate: "192k"},
		{Name: "480", Resolution: 480, VideoBitrate: "1500k", MaxRate: "2000k", BufSize: "3000k", AudioBitrate: "128k"},
	}
	smallLadder := core.Ladder{
		{Name: "360", Resolution: 360, VideoBitrate: "800k", MaxRate: "1000k", BufSize: "1600k", AudioBitrate: "96k"},
	}

	// two encoders draining the same queue, like ConcurrentEncodings = 2
	rig := newTestRig(context.Background(), t, newTestOptions(), make(chan *os.File, 2), 2)

	bigJob := newTestJob(t, writeTestVideo(t), bigLadder)
	bigJob.DispatchJob.KeyStarter = "test/big"
	smallJob := newTestJob(t, writeTestVideo(t), smallLadder)
	smallJob.DispatchJob.KeyStarter = "test/small"

	rig.encodeQueue <- bigJob
	rig.encodeQueue <- smallJob

	results := map[string]*core.Job{}
	for range 2 {
		dispatched := rig.waitDispatchedJob(t)
		results[dispatched.DispatchJob.KeyStarter] = dispatched
	}

	// each job's output dir must contain exactly its own variants,
	// never a mix of both ladders
	countVariants := func(dir string) int {
		matches, err := filepath.Glob(filepath.Join(dir, "playlist_*.m3u8"))
		if err != nil {
			t.Fatalf("failed to glob playlists: %v", err)
		}
		return len(matches)
	}

	if got := countVariants(results["test/big"].DispatchJob.FromDir); got != 3 {
		t.Errorf("expected 3 variants for the big ladder job, got %v", got)
	}
	if got := countVariants(results["test/small"].DispatchJob.FromDir); got != 1 {
		t.Errorf("expected 1 variant for the small ladder job, got %v", got)
	}

	// and each ffmpeg invocation used only its own ladder's scales
	invocations := readInvocations(t, argsDir)
	if len(invocations) != 2 {
		t.Fatalf("expected 2 ffmpeg invocations, got %v", len(invocations))
	}
	for _, args := range invocations {
		filterComplex := flagValue(args, "-filter_complex")
		hasBig := strings.Contains(filterComplex, "scale=-2:1080")
		hasSmall := strings.Contains(filterComplex, "scale=-2:360")
		if hasBig == hasSmall {
			t.Errorf("each invocation must use exactly one ladder, got %q", filterComplex)
		}
	}

	rig.finish()
}

func TestEncoderCancellationCleansUpAndDispatchesNothing(t *testing.T) {
	workDir := chdir(t)
	argsDir := fakeFFmpeg(t)
	t.Setenv("FFMPEG_FAKE_SLEEP", "30")
	writeFakeMetadata(t, metadataWithStreams(
		stream{codecType: "video", width: 1920, height: 1080},
		stream{codecType: "audio"},
	))

	ctx, cancel := context.WithCancel(context.Background())
	rig := newTestRig(ctx, t, newTestOptions(), make(chan *os.File, 1), 1)

	rig.encodeQueue <- newTestJob(t, writeTestVideo(t), nil)

	// wait until ffmpeg actually started, then cancel
	deadline := time.Now().Add(10 * time.Second)
	for {
		if entries, _ := filepath.Glob(filepath.Join(argsDir, "*.txt")); len(entries) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake ffmpeg never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()

	rig.finish()

	select {
	case job := <-rig.dispatchQueue:
		t.Fatalf("a canceled job must not be dispatched, got %v", job.DispatchJob.KeyStarter)
	default:
	}

	leftovers, err := filepath.Glob(filepath.Join(workDir, "output", "video-*"))
	if err != nil {
		t.Fatalf("failed to glob output dir: %v", err)
	}
	if len(leftovers) != 0 {
		t.Errorf("expected the canceled job's output dir to be cleaned up, found %v", leftovers)
	}
}

func TestEncoderReturnsDownloadedFileToThePool(t *testing.T) {
	chdir(t)
	fakeFFmpeg(t)
	writeFakeMetadata(t, metadataWithStreams(
		stream{codecType: "video", width: 1280, height: 720},
	))

	filePool := make(chan *os.File, 1)
	rig := newTestRig(context.Background(), t, newTestOptions(), filePool, 1)

	job := newTestJob(t, writeTestVideo(t), nil)
	job.EncodeJob.DownloadedFile = true

	rig.encodeQueue <- job
	rig.waitDispatchedJob(t)

	// the file is returned by a deferred call, which runs right after
	// the job lands on the dispatch queue
	select {
	case returned := <-filePool:
		if returned.Name() != job.EncodeJob.File.Name() {
			t.Errorf("expected the same file to return to the pool, got %v", returned.Name())
		}
	case <-time.After(5 * time.Second):
		t.Error("expected the downloaded file to be returned to the file pool")
	}

	rig.finish()
}

// a retried job must reuse the ladder pinned on its first pass,
// even if the configured ladder changed in between
func TestEncoderRetryUsesPinnedLadder(t *testing.T) {
	chdir(t)
	argsDir := fakeFFmpeg(t)
	writeFakeMetadata(t, metadataWithStreams(
		stream{codecType: "video", width: 1920, height: 1080},
		stream{codecType: "audio"},
	))

	options := newTestOptions()
	options.Encode.Ladder = core.Ladder{
		{Name: "720", Resolution: 720, VideoBitrate: "3500k", MaxRate: "5000k", BufSize: "7000k", AudioBitrate: "192k"},
	}

	rig := newTestRig(context.Background(), t, options, make(chan *os.File, 1), 1)

	rig.encodeQueue <- newTestJob(t, writeTestVideo(t), nil)
	first := rig.waitDispatchedJob(t)

	// the config ladder changes before the retry comes in
	options.Encode.Ladder = core.Ladder{
		{Name: "1080", Resolution: 1080, VideoBitrate: "6000k", MaxRate: "8000k", BufSize: "12000k", AudioBitrate: "192k"},
		{Name: "480", Resolution: 480, VideoBitrate: "1500k", MaxRate: "2000k", BufSize: "3000k", AudioBitrate: "128k"},
	}
	rig.encodeQueue <- first
	rig.waitDispatchedJob(t)

	invocations := readInvocations(t, argsDir)
	if len(invocations) != 2 {
		t.Fatalf("expected 2 ffmpeg invocations, got %v", len(invocations))
	}
	for index, args := range invocations {
		filterComplex := flagValue(args, "-filter_complex")
		if !strings.Contains(filterComplex, "split=1") || !strings.Contains(filterComplex, "scale=-2:720") {
			t.Errorf("invocation %v should have reused the pinned 720-only ladder, got %q", index, filterComplex)
		}
	}

	rig.finish()
}
