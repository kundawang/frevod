package core

import "os"

type Job struct {
	IngestJob   IngestJob
	EncodeJob   EncodeJob
	DispatchJob DispatchJob
}

func NewJob() *Job {
	return &Job{
		IngestJob:   IngestJob{},
		EncodeJob:   EncodeJob{},
		DispatchJob: DispatchJob{},
	}
}

type IngestJob struct {
	VideoUri     string
	UriType      URIType
	Source       string
	UploadMethod string
}

type EncodeJob struct {
	AbsoluteVideoPath string
	File              *os.File
	DownloadedFile    bool

	// Ladder is the output ladder requested for this job.
	// A nil Ladder means "use the configured/default ladder".
	// The encoder resolves it once and pins the result here, so
	// retries of this job always reuse the exact same rungs.
	Ladder Ladder
}

type DispatchJob struct {
	FromDir    string
	KeyStarter string
}
