package encoder

import (
	"slices"
	"testing"

	"github.com/Matheus-Rossetti/frevod/internal/core"
)

func rungResolutions(ladder core.Ladder) []int {
	resolutions := make([]int, 0, len(ladder))
	for _, rung := range ladder {
		resolutions = append(resolutions, rung.Resolution)
	}
	return resolutions
}

func TestResolveLadder(t *testing.T) {
	testCases := []struct {
		name                string
		ladder              core.Ladder
		referenceResolution int
		expectedResolutions []int
	}{
		{
			name:                "nil ladder keeps the original default ladder",
			ladder:              nil,
			referenceResolution: 1080,
			// 2160 and 1440 are filtered out, never upscale
			expectedResolutions: []int{1080, 720, 480},
		},
		{
			name:                "nil ladder on a 4k source keeps every default rung",
			ladder:              nil,
			referenceResolution: 2160,
			expectedResolutions: []int{2160, 1440, 1080, 720, 480},
		},
		{
			name: "duplicate rungs are removed, first occurrence wins",
			ladder: core.Ladder{
				{Name: "720", Resolution: 720, VideoBitrate: "3500k", MaxRate: "5000k", BufSize: "7000k", AudioBitrate: "192k"},
				{Name: "720-copy", Resolution: 720, VideoBitrate: "9999k", MaxRate: "9999k", BufSize: "9999k", AudioBitrate: "320k"},
				{Name: "480", Resolution: 480, VideoBitrate: "1500k", MaxRate: "2000k", BufSize: "3000k", AudioBitrate: "128k"},
			},
			referenceResolution: 1080,
			expectedResolutions: []int{720, 480},
		},
		{
			name: "ladder given in ascending order comes out normalized, highest first",
			ladder: core.Ladder{
				{Name: "480", Resolution: 480, VideoBitrate: "1500k", MaxRate: "2000k", BufSize: "3000k", AudioBitrate: "128k"},
				{Name: "1080", Resolution: 1080, VideoBitrate: "6000k", MaxRate: "8000k", BufSize: "12000k", AudioBitrate: "192k"},
				{Name: "720", Resolution: 720, VideoBitrate: "3500k", MaxRate: "5000k", BufSize: "7000k", AudioBitrate: "192k"},
			},
			referenceResolution: 2160,
			expectedResolutions: []int{1080, 720, 480},
		},
		{
			name: "every rung filtered out falls back to the lowest rung",
			ladder: core.Ladder{
				{Name: "1080", Resolution: 1080, VideoBitrate: "6000k", MaxRate: "8000k", BufSize: "12000k", AudioBitrate: "192k"},
				{Name: "720", Resolution: 720, VideoBitrate: "3500k", MaxRate: "5000k", BufSize: "7000k", AudioBitrate: "192k"},
			},
			referenceResolution: 360,
			expectedResolutions: []int{720},
		},
		{
			name: "invalid rungs are dropped",
			ladder: core.Ladder{
				{Name: "broken", Resolution: 0, VideoBitrate: "6000k", MaxRate: "8000k", BufSize: "12000k", AudioBitrate: "192k"},
				{Name: "720", Resolution: 720, VideoBitrate: "3500k", MaxRate: "5000k", BufSize: "7000k", AudioBitrate: "192k"},
			},
			referenceResolution: 1080,
			expectedResolutions: []int{720},
		},
		{
			name: "a ladder with only invalid rungs falls back to the default ladder",
			ladder: core.Ladder{
				{Name: "broken", Resolution: -1},
			},
			referenceResolution: 480,
			expectedResolutions: []int{480},
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			resolved := ResolveLadder(tt.ladder, tt.referenceResolution)

			if !slices.Equal(rungResolutions(resolved), tt.expectedResolutions) {
				t.Errorf("expected resolutions %v, got %v", tt.expectedResolutions, rungResolutions(resolved))
			}
		})
	}
}

func TestResolveLadderDuplicateKeepsFirstOccurrence(t *testing.T) {
	ladder := core.Ladder{
		{Name: "720", Resolution: 720, VideoBitrate: "3500k", MaxRate: "5000k", BufSize: "7000k", AudioBitrate: "192k"},
		{Name: "720-copy", Resolution: 720, VideoBitrate: "9999k", MaxRate: "9999k", BufSize: "9999k", AudioBitrate: "320k"},
	}

	resolved := ResolveLadder(ladder, 720)

	if len(resolved) != 1 {
		t.Fatalf("expected exactly one rung, got %v", len(resolved))
	}
	if resolved[0].Name != "720" || resolved[0].VideoBitrate != "3500k" {
		t.Errorf("expected the first occurrence to win, got %+v", resolved[0])
	}
}

func TestNewVideoOrientationAndRenditions(t *testing.T) {
	testCases := []struct {
		name                 string
		metadata             *Metadata
		ladder               core.Ladder
		expectedOrientation  string
		expectedReferenceRes int
		expectedResolutions  []int
		expectedHasAudio     bool
	}{
		{
			name: "vertical video uses width as reference resolution",
			metadata: metadataWithStreams(
				stream{codecType: "video", width: 1080, height: 1920},
				stream{codecType: "audio"},
			),
			ladder:               nil,
			expectedOrientation:  "vertical",
			expectedReferenceRes: 1080,
			expectedResolutions:  []int{1080, 720, 480},
			expectedHasAudio:     true,
		},
		{
			name: "horizontal video uses height as reference resolution",
			metadata: metadataWithStreams(
				stream{codecType: "video", width: 1920, height: 1080},
				stream{codecType: "audio"},
			),
			ladder:               nil,
			expectedOrientation:  "horizontal",
			expectedReferenceRes: 1080,
			expectedResolutions:  []int{1080, 720, 480},
			expectedHasAudio:     true,
		},
		{
			name: "video without an audio stream",
			metadata: metadataWithStreams(
				stream{codecType: "video", width: 1280, height: 720},
			),
			ladder:               nil,
			expectedOrientation:  "horizontal",
			expectedReferenceRes: 720,
			expectedResolutions:  []int{720, 480},
			expectedHasAudio:     false,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			video := NewVideo(tt.metadata, tt.ladder)

			if video.Orientation != tt.expectedOrientation {
				t.Errorf("expected orientation %v, got %v", tt.expectedOrientation, video.Orientation)
			}
			if video.ReferenceResolution != tt.expectedReferenceRes {
				t.Errorf("expected reference resolution %v, got %v", tt.expectedReferenceRes, video.ReferenceResolution)
			}
			if !slices.Equal(rungResolutions(video.Renditions), tt.expectedResolutions) {
				t.Errorf("expected renditions %v, got %v", tt.expectedResolutions, rungResolutions(video.Renditions))
			}
			if video.HasAudio != tt.expectedHasAudio {
				t.Errorf("expected hasAudio to be %v, got %v", tt.expectedHasAudio, video.HasAudio)
			}
		})
	}
}
