package bagit

import (
	"encoding/json"
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	checkCompletenessCommand  = "check_completeness"
	validateStrictCommand     = "validate_strict"
	maxCompletenessReplyBytes = 16 * 1024
	maxCompletenessExamples   = 10
)

// A checksum mismatch needs room for a full JSON-escaped Windows long path.
const maxStrictReplyBytes = 256 * 1024

// DifferenceKind identifies how a path differs from the bag's manifests.
type DifferenceKind string

const (
	// DifferenceMissing identifies a manifest entry absent from the bag.
	DifferenceMissing DifferenceKind = "missing"
	// DifferenceUnexpected identifies a payload file absent from the required manifests.
	DifferenceUnexpected DifferenceKind = "unexpected"
)

// PathDifference describes a missing or unexpected bag-relative path.
// Directory separators are reported as slashes.
type PathDifference struct {
	Kind DifferenceKind
	Path string
}

// FileDifferences contains exact counts and a bounded sample of paths.
type FileDifferences struct {
	Missing    int64
	Unexpected int64
	Examples   []PathDifference
}

// CompletenessError reports a bag-format or completeness failure.
// Differences is nil when a reliable comparison was unavailable or did not
// apply. Zero counts indicate that the comparison found no path differences.
type CompletenessError struct {
	Summary     string
	Differences *FileDifferences
}

func (e *CompletenessError) Error() string {
	var msg strings.Builder
	msg.WriteString("invalid: ")
	quoted := strconv.QuoteToGraphic(e.Summary)
	msg.WriteString(quoted[1 : len(quoted)-1])
	if diff := e.Differences; diff != nil {
		fmt.Fprintf(&msg, " (%d missing, %d unexpected)", diff.Missing, diff.Unexpected)
		for _, example := range diff.Examples {
			fmt.Fprintf(&msg, " [%s %q]", example.Kind, example.Path)
		}
	}
	return msg.String()
}

// Unwrap returns ErrInvalid so callers can use errors.Is for the verdict.
func (e *CompletenessError) Unwrap() error { return ErrInvalid }

// CheckCompleteness checks the bag's structure and manifest membership without
// verifying checksums or modifying the bag. It returns a *CompletenessError
// for an invalid bag. I/O and runner failures return separate errors.
// The bag path must be valid UTF-8.
func (b *BagIt) CheckCompleteness(path string) error {
	return b.checkBag(checkCompletenessCommand, path, 0)
}

func (b *BagIt) checkBag(command, path string, hashWorkers int) error {
	if !utf8.ValidString(path) {
		operation, _ := checkLabels(command)
		return fmt.Errorf("%s: bag path must be valid UTF-8", operation)
	}
	blob, err := b.send(command, &validateRequest{Path: path, HashWorkers: hashWorkers})
	if err != nil {
		return err
	}
	return decodeCheckResponse(command, blob)
}

func checkLabels(command string) (operation, response string) {
	if command == validateStrictCommand {
		return "validate strict", "strict validation"
	}
	return "check completeness", "completeness"
}

func decodeCheckResponse(command string, blob []byte) error {
	operation, response := checkLabels(command)
	decode := "decode " + response + " response"
	var reply struct {
		Status      string          `json:"status"`
		Message     json.RawMessage `json:"message"`
		Differences json.RawMessage `json:"differences"`
		LegacyType  json.RawMessage `json:"type"`
	}
	if err := json.Unmarshal(blob, &reply); err != nil {
		return fmt.Errorf("%s: %w", decode, err)
	}
	isMismatch := reply.Status == "checksum_mismatch"
	if isMismatch && command != validateStrictCommand {
		return fmt.Errorf("%s: unknown status %q", decode, reply.Status)
	}
	if !isMismatch && len(blob)+1 > maxCompletenessReplyBytes {
		return fmt.Errorf("%s: reply exceeds %d bytes", decode, maxCompletenessReplyBytes)
	}

	var message string
	switch reply.Status {
	case "invalid", "error", "checksum_mismatch":
		if json.Unmarshal(reply.Message, &message) != nil || message == "" {
			return fmt.Errorf("%s: %s status requires a message", decode, reply.Status)
		}
	}
	if (reply.Status == "error" || isMismatch) && reply.Differences != nil {
		return fmt.Errorf("%s: %s status with path differences", decode, reply.Status)
	}

	switch reply.Status {
	case "complete":
		if reply.Message != nil || reply.Differences != nil {
			return fmt.Errorf("%s: complete status with failure details", decode)
		}
		return nil
	case "invalid":
		differences, err := decodeFileDifferences(reply.Differences)
		if err != nil {
			return fmt.Errorf("%s: %w", decode, err)
		}
		return &CompletenessError{Summary: message, Differences: differences}
	case "error":
		return fmt.Errorf("%s: %s", operation, message)
	case "checksum_mismatch":
		return fmt.Errorf("%w: %s", ErrInvalid, message)
	case "":
		var legacyType string
		if json.Unmarshal(reply.LegacyType, &legacyType) == nil && legacyType == "UnknownCommandError" {
			return fmt.Errorf("%s: incompatible Python runner does not support %s", operation, command)
		}
		return fmt.Errorf("%s: missing status", decode)
	default:
		return fmt.Errorf("%s: unknown status %q", decode, reply.Status)
	}
}

func decodeFileDifferences(blob json.RawMessage) (*FileDifferences, error) {
	if blob == nil {
		return nil, nil
	}
	var wire struct {
		Missing    *int64            `json:"missing"`
		Unexpected *int64            `json:"unexpected"`
		Examples   *[]PathDifference `json:"examples"`
	}
	if err := json.Unmarshal(blob, &wire); err != nil {
		return nil, err
	}
	if wire.Missing == nil || wire.Unexpected == nil || wire.Examples == nil {
		return nil, fmt.Errorf("path differences require both counts and an examples array")
	}
	if *wire.Missing < 0 || *wire.Unexpected < 0 || len(*wire.Examples) > maxCompletenessExamples {
		return nil, fmt.Errorf("invalid path difference counts or sample size")
	}

	remaining := map[DifferenceKind]int64{DifferenceMissing: *wire.Missing, DifferenceUnexpected: *wire.Unexpected}
	seen := make(map[string]bool, len(*wire.Examples))
	for _, example := range *wire.Examples {
		p := example.Path
		if p == "" || path.Clean(p) != p || path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") || strings.ContainsRune(p, 0) {
			return nil, fmt.Errorf("invalid path in difference example")
		}
		if seen[p] || remaining[example.Kind] == 0 {
			return nil, fmt.Errorf("inconsistent path difference example")
		}
		seen[p] = true
		remaining[example.Kind]--
	}

	return &FileDifferences{
		Missing:    *wire.Missing,
		Unexpected: *wire.Unexpected,
		Examples:   *wire.Examples,
	}, nil
}
