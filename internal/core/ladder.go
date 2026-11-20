package core

// Rung is a single tier of the output ladder:
// one resolution plus the bitrates used to encode it.
//
// Resolution is measured in reference pixels: the height for
// horizontal videos and the width for vertical/square ones, so the
// same ladder works for any orientation.
type Rung struct {
	Name         string `yaml:"name"          json:"name"`
	Resolution   int    `yaml:"resolution"    json:"resolution"`
	VideoBitrate string `yaml:"video_bitrate" json:"video_bitrate"` // average bitrate, e.g. "6000k"
	MaxRate      string `yaml:"maxrate"       json:"maxrate"`
	BufSize      string `yaml:"bufsize"       json:"bufsize"`
	AudioBitrate string `yaml:"audio_bitrate" json:"audio_bitrate"`
}

// Ladder is the set of rungs a job may be encoded into.
type Ladder []Rung

// Valid reports whether a rung has everything the encoder needs
// to build an FFmpeg command from it.
func (r Rung) Valid() bool {
	return r.Resolution > 0 &&
		r.Name != "" &&
		r.VideoBitrate != "" &&
		r.MaxRate != "" &&
		r.BufSize != "" &&
		r.AudioBitrate != ""
}

// DefaultLadder is the historical Frevod ladder. It is used whenever
// neither the config file nor the job itself specify a ladder, so
// jobs that don't ask for anything keep the original behavior.
func DefaultLadder() Ladder {
	return Ladder{
		{Name: "2160", Resolution: 2160, VideoBitrate: "25000k", MaxRate: "35000k", BufSize: "50000k", AudioBitrate: "320k"},
		{Name: "1440", Resolution: 1440, VideoBitrate: "12000k", MaxRate: "16000k", BufSize: "24000k", AudioBitrate: "320k"},
		{Name: "1080", Resolution: 1080, VideoBitrate: "6000k", MaxRate: "8000k", BufSize: "12000k", AudioBitrate: "192k"},
		{Name: "720", Resolution: 720, VideoBitrate: "3500k", MaxRate: "5000k", BufSize: "7000k", AudioBitrate: "192k"},
		{Name: "480", Resolution: 480, VideoBitrate: "1500k", MaxRate: "2000k", BufSize: "3000k", AudioBitrate: "128k"},
	}
}
