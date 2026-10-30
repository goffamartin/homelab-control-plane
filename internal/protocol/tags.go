package protocol

import "errors"

const (
	MaxResourceTags = 16
	MaxTagLength    = 64
)

var ErrInvalidTags = errors.New("hlcp: tags must be unique lowercase identifiers, at most 16 tags of 64 bytes")

// ValidTag permits safe, case-sensitive metadata identifiers, never expressions.
func ValidTag(tag string) bool {
	if len(tag) == 0 || len(tag) > MaxTagLength {
		return false
	}
	for i, c := range tag {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			continue
		}
		if i == 0 || c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

func ValidateTags(tags []string) error {
	if len(tags) > MaxResourceTags {
		return ErrInvalidTags
	}
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		if !ValidTag(tag) || seen[tag] {
			return ErrInvalidTags
		}
		seen[tag] = true
	}
	return nil
}

// MatchesTags is an AND filter. Empty filters include untagged resources too.
func MatchesTags(tags, required []string) bool {
	for _, wanted := range required {
		found := false
		for _, tag := range tags {
			if tag == wanted {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
