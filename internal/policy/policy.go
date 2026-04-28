package policy

import "fmt"

type ArtifactRule struct {
	MaxSizeBytes int64
	MediaTypes   map[string]bool
}

func AllowArtifact(size int64, mediaType string, rule ArtifactRule) error {
	if rule.MaxSizeBytes > 0 && size > rule.MaxSizeBytes {
		return fmt.Errorf("artifact exceeds max size")
	}
	if len(rule.MediaTypes) > 0 && !rule.MediaTypes[mediaType] {
		return fmt.Errorf("media type %q is not allowed", mediaType)
	}
	return nil
}

func RejectRawArtifactMetadata(metadata map[string]any) error {
	for key, value := range metadata {
		switch key {
		case "raw", "raw_payload", "binary", "bytes", "base64", "data_uri":
			return fmt.Errorf("artifact metadata contains raw payload field %q", key)
		}
		if nested, ok := value.(map[string]any); ok {
			if err := RejectRawArtifactMetadata(nested); err != nil {
				return err
			}
		}
	}
	return nil
}
