package rest

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/Matheus-Rossetti/frevod/internal/core"
)

func TestValidateBody(t *testing.T) {
	testCases := []struct {
		name               string
		body               string
		expectedUriType    core.URIType
		expectedUri        string
		expectedKeyStarter string
		expectedLadder     core.Ladder
		wantErr            bool
	}{
		{
			name:               "valid body",
			body:               `{"uri": "http://mycoolvideo.com", "key_starter": "key/starter"}`,
			expectedUriType:    core.Url,
			expectedUri:        "http://mycoolvideo.com",
			expectedKeyStarter: "key/starter",
			expectedLadder:     nil,
			wantErr:            false,
		},
		{
			name:               "empty body",
			body:               ``,
			expectedUriType:    core.Unsupported,
			expectedUri:        "",
			expectedKeyStarter: "",
			wantErr:            true,
		},
		{
			name:               "lacking key_starter",
			body:               `{"uri": "http://mycoolvideo.com"}`,
			expectedUriType:    core.Unsupported,
			expectedUri:        "",
			expectedKeyStarter: "",
			wantErr:            true,
		},
		{
			name:               "unsupported uri",
			body:               `{"uri": "ftp://mycoolvideo.com", "key_starter": "key/starter"}`,
			expectedUriType:    core.Unsupported,
			expectedUri:        "",
			expectedKeyStarter: "",
			wantErr:            true,
		},
		{
			name: "valid custom ladder",
			body: `{
				"uri": "http://mycoolvideo.com",
				"key_starter": "key/starter",
				"ladder": [
					{"name": "1080", "resolution": 1080, "video_bitrate": "6000k", "maxrate": "8000k", "bufsize": "12000k", "audio_bitrate": "192k"},
					{"name": "360", "resolution": 360, "video_bitrate": "800k", "maxrate": "1000k", "bufsize": "1600k", "audio_bitrate": "96k"}
				]
			}`,
			expectedUriType:    core.Url,
			expectedUri:        "http://mycoolvideo.com",
			expectedKeyStarter: "key/starter",
			expectedLadder: core.Ladder{
				{Name: "1080", Resolution: 1080, VideoBitrate: "6000k", MaxRate: "8000k", BufSize: "12000k", AudioBitrate: "192k"},
				{Name: "360", Resolution: 360, VideoBitrate: "800k", MaxRate: "1000k", BufSize: "1600k", AudioBitrate: "96k"},
			},
			wantErr: false,
		},
		{
			name: "ladder rung missing bitrates",
			body: `{
				"uri": "http://mycoolvideo.com",
				"key_starter": "key/starter",
				"ladder": [
					{"name": "1080", "resolution": 1080}
				]
			}`,
			expectedUriType:    core.Unsupported,
			expectedUri:        "",
			expectedKeyStarter: "",
			wantErr:            true,
		},
		{
			name: "ladder rung with invalid resolution",
			body: `{
				"uri": "http://mycoolvideo.com",
				"key_starter": "key/starter",
				"ladder": [
					{"name": "zero", "resolution": 0, "video_bitrate": "6000k", "maxrate": "8000k", "bufsize": "12000k", "audio_bitrate": "192k"}
				]
			}`,
			expectedUriType:    core.Unsupported,
			expectedUri:        "",
			expectedKeyStarter: "",
			wantErr:            true,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			body := io.NopCloser(strings.NewReader(tt.body))

			uriType, uri, keyStarter, ladder, err := validateBody(body)

			if tt.expectedUriType != uriType {
				t.Errorf("expected uri type to be %v, got %v", tt.expectedUri, uriType)
			}

			if tt.expectedUri != uri {
				t.Errorf("expected uri to be %v, got %v", tt.expectedUri, uri)
			}

			if tt.expectedKeyStarter != keyStarter {
				t.Errorf("expected key starter to be %v, got %v", tt.expectedKeyStarter, keyStarter)
			}

			if !slices.Equal(tt.expectedLadder, ladder) {
				t.Errorf("expected ladder to be %v, got %v", tt.expectedLadder, ladder)
			}

			hasErr := (err != nil)
			if tt.wantErr != hasErr {
				t.Errorf("expected error to be %v, got %v", tt.wantErr, hasErr)
			}
		})
	}
}
