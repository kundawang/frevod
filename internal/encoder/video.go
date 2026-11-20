package encoder

import (
	"slices"

	"github.com/Matheus-Rossetti/frevod/internal/core"
)

type Video struct {
	Height      int
	Width       int
	AspectRatio string
	Duration    string // Seconds

	HasAudio            bool // Useful when building the ffmpeg command, we dont include audio tags if hasAudio == false
	Orientation         string
	ReferenceResolution int         // in a vertical video, this is width value | for horizontal, height value
	Renditions          core.Ladder // if a video is 1440p/QHD with the default ladder, this would be -> 1440p, 1080p, 720p, 480p
}

func NewVideo(metadata *Metadata, ladder core.Ladder) *Video {

	hasAudio := false
	videoHeight := 0
	mainStream := metadata.Streams[0]
	for index, stream := range metadata.Streams {

		// mainStream should be the one with the highest res
		if stream.Height > videoHeight {
			mainStream = metadata.Streams[index]
		}

		if stream.CodecType == "audio" {
			hasAudio = true
		}
	}

	// --------- CHECK VIDEO ORIENTATION ---------
	var orientation string
	var referenceResolution int
	if mainStream.Width > mainStream.Height {
		orientation = "horizontal"
		referenceResolution = mainStream.Height
	} else {
		orientation = "vertical" // vertical also covers square videos.
		referenceResolution = mainStream.Width
	}

	return &Video{
		Height:      mainStream.Height,
		Width:       mainStream.Width,
		AspectRatio: mainStream.AspectRatio,
		Duration:    mainStream.Duration,

		HasAudio:            hasAudio,
		Orientation:         orientation,
		ReferenceResolution: referenceResolution,
		Renditions:          ResolveLadder(ladder, referenceResolution),
	}
}

// ResolveLadder turns a requested ladder into the final, ordered set of
// renditions for a source whose reference resolution is referenceResolution.
//
// The result is deterministic no matter how the input was ordered:
//  1. invalid rungs are dropped (empty ladder falls back to DefaultLadder)
//  2. rungs are sorted by resolution, highest first, so input given in
//     any order (e.g. bitrates listed low-to-high) comes out normalized
//  3. duplicate resolutions are removed, the first occurrence wins
//  4. rungs above the source reference resolution are filtered out,
//     we never upscale
//  5. if every rung was filtered out, the lowest rung is kept,
//     so a job always produces at least one rendition
func ResolveLadder(ladder core.Ladder, referenceResolution int) core.Ladder {

	if len(ladder) == 0 {
		ladder = core.DefaultLadder()
	}

	valid := make(core.Ladder, 0, len(ladder))
	for _, rung := range ladder {
		if rung.Valid() {
			valid = append(valid, rung)
		}
	}
	if len(valid) == 0 {
		valid = core.DefaultLadder()
	}

	// highest resolution first, stable so duplicates keep input order
	sorted := slices.Clone(valid)
	slices.SortStableFunc(sorted, func(a, b core.Rung) int {
		return b.Resolution - a.Resolution
	})

	deduped := make(core.Ladder, 0, len(sorted))
	seen := make(map[int]struct{}, len(sorted))
	for _, rung := range sorted {
		if _, ok := seen[rung.Resolution]; ok {
			continue
		}
		seen[rung.Resolution] = struct{}{}
		deduped = append(deduped, rung)
	}

	// only adds renditions of native res and lower, never upscale.
	var renditions core.Ladder
	for _, rung := range deduped {
		if referenceResolution >= rung.Resolution {
			renditions = append(renditions, rung)
		}
	}

	// in case every rung sits above the source res
	if len(renditions) == 0 {
		renditions = append(renditions, deduped[len(deduped)-1]) // add the lowest possible res to the list
	}

	return renditions
}
