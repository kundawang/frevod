package rest

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Matheus-Rossetti/frevod/internal/core"
)

func validateBody(body io.ReadCloser) (core.URIType, string, string, core.Ladder, error) {

	var request struct {
		Uri        string      `json:"uri"`
		KeyStarter string      `json:"key_starter"`
		Ladder     core.Ladder `json:"ladder"` // optional, nil means "use the configured/default ladder"
	}

	err := json.NewDecoder(body).Decode(&request)
	if err != nil {
		return core.Unsupported,
			"",
			"",
			nil,
			fmt.Errorf("invalid json")
	}

	if request.Uri == "" || request.KeyStarter == "" {
		return core.Unsupported,
			"",
			"",
			nil,
			fmt.Errorf("needs uri and key_starter")
	}

	uriType, scheme := core.CategorizeUri(request.Uri)
	if uriType == core.Unsupported {
		return uriType,
			"",
			"",
			nil,
			fmt.Errorf("unsupported uri scheme (%v), supported schemes include: http, https, file and local paths.", scheme)
	}

	for index, rung := range request.Ladder {
		if !rung.Valid() {
			return core.Unsupported,
				"",
				"",
				nil,
				fmt.Errorf("invalid ladder rung at index %v: every rung needs a name, a resolution > 0, video_bitrate, maxrate, bufsize and audio_bitrate", index)
		}
	}

	return uriType, request.Uri, request.KeyStarter, request.Ladder, nil
}
