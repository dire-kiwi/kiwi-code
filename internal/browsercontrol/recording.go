package browsercontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const MaxRecordingBytes int64 = 1 << 30

var (
	ErrRecordingNotFound            = errors.New("browser recording not found")
	ErrRecordingRangeNotSatisfiable = errors.New("browser recording range is not satisfiable")
)

// RecordingRangeError carries the validated total size returned with a 416.
type RecordingRangeError struct{ Size int64 }

func (e *RecordingRangeError) Error() string { return ErrRecordingRangeNotSatisfiable.Error() }
func (e *RecordingRangeError) Unwrap() error { return ErrRecordingRangeNotSatisfiable }

type recordingRequest struct {
	ProjectID   string `json:"projectId"`
	ThreadID    string `json:"threadId"`
	RecordingID string `json:"recordingId"`
}

type RecordingRange struct {
	Start int64
	End   int64
	Size  int64
	Total int64
}

// ResolveRecordingRange validates one HTTP byte range. An empty header selects
// the complete recording. Multiple ranges are deliberately unsupported.
func ResolveRecordingRange(header string, total int64) (RecordingRange, error) {
	if total <= 0 || total > MaxRecordingBytes {
		return RecordingRange{}, ErrInvalidResponse
	}
	if strings.TrimSpace(header) == "" {
		return RecordingRange{Start: 0, End: total - 1, Size: total, Total: total}, nil
	}
	header = strings.TrimSpace(header)
	if len(header) > 128 || strings.ContainsAny(header, "\r\n\x00") {
		return RecordingRange{}, ErrRecordingRangeNotSatisfiable
	}
	separator := strings.IndexByte(header, '=')
	if separator < 0 || !strings.EqualFold(header[:separator], "bytes") {
		return RecordingRange{}, ErrRecordingRangeNotSatisfiable
	}
	value := header[separator+1:]
	if strings.Contains(value, ",") || strings.Count(value, "-") != 1 {
		return RecordingRange{}, ErrRecordingRangeNotSatisfiable
	}
	parts := strings.SplitN(value, "-", 2)
	if parts[0] == "" && parts[1] == "" {
		return RecordingRange{}, ErrRecordingRangeNotSatisfiable
	}
	parse := func(value string) (int64, error) {
		if value == "" {
			return -1, nil
		}
		if strings.Trim(value, "0123456789") != "" {
			return 0, ErrRecordingRangeNotSatisfiable
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed < 0 {
			return 0, ErrRecordingRangeNotSatisfiable
		}
		return parsed, nil
	}
	first, err := parse(parts[0])
	if err != nil {
		return RecordingRange{}, err
	}
	last, err := parse(parts[1])
	if err != nil {
		return RecordingRange{}, err
	}
	var start, end int64
	if first < 0 {
		if last <= 0 {
			return RecordingRange{}, ErrRecordingRangeNotSatisfiable
		}
		if last > total {
			last = total
		}
		start, end = total-last, total-1
	} else {
		if first >= total {
			return RecordingRange{}, ErrRecordingRangeNotSatisfiable
		}
		start, end = first, total-1
		if last >= 0 {
			if last < start {
				return RecordingRange{}, ErrRecordingRangeNotSatisfiable
			}
			if last < end {
				end = last
			}
		}
	}
	return RecordingRange{Start: start, End: end, Size: end - start + 1, Total: total}, nil
}

func parseRecordingContentRange(header string) (RecordingRange, error) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bytes") {
		return RecordingRange{}, ErrInvalidResponse
	}
	sections := strings.Split(fields[1], "/")
	if len(sections) != 2 {
		return RecordingRange{}, ErrInvalidResponse
	}
	bounds := strings.Split(sections[0], "-")
	if len(bounds) != 2 {
		return RecordingRange{}, ErrInvalidResponse
	}
	start, startErr := strconv.ParseInt(bounds[0], 10, 64)
	end, endErr := strconv.ParseInt(bounds[1], 10, 64)
	total, totalErr := strconv.ParseInt(sections[1], 10, 64)
	if startErr != nil || endErr != nil || totalErr != nil || start < 0 || end < start || total <= end || total > MaxRecordingBytes {
		return RecordingRange{}, ErrInvalidResponse
	}
	return RecordingRange{Start: start, End: end, Size: end - start + 1, Total: total}, nil
}

func parseUnsatisfiedRecordingRange(header string) (int64, error) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bytes") || !strings.HasPrefix(fields[1], "*/") {
		return 0, ErrInvalidResponse
	}
	total, err := strconv.ParseInt(strings.TrimPrefix(fields[1], "*/"), 10, 64)
	if err != nil || total <= 0 || total > MaxRecordingBytes {
		return 0, ErrInvalidResponse
	}
	return total, nil
}

func (c *Client) recordingConfig() (providerConfig, error) {
	for _, configPath := range c.configPaths {
		config, err := loadConfig(configPath)
		if err == nil {
			return config, nil
		}
	}
	return providerConfig{}, fmt.Errorf("%w: configuration is not available", ErrUnavailable)
}

func (c *Client) OpenRecordingRange(ctx context.Context, projectID, threadID, recordingID, rangeHeader string) (Recording, error) {
	if strings.TrimSpace(rangeHeader) != "" {
		// The maximum possible file size is also a safe syntax-validation total.
		// A start beyond it can never identify a satisfiable provider range.
		if _, err := ResolveRecordingRange(rangeHeader, MaxRecordingBytes); err != nil {
			return Recording{}, err
		}
		rangeHeader = strings.TrimSpace(rangeHeader)
	}

	config, err := c.recordingConfig()
	if err != nil {
		return Recording{}, err
	}
	body, err := json.Marshal(recordingRequest{ProjectID: projectID, ThreadID: threadID, RecordingID: recordingID})
	if err != nil || len(body) > MaxRequestBytes {
		return Recording{}, ErrRequestTooLarge
	}
	endpoint := "http://127.0.0.1:" + strconv.Itoa(config.Port) + "/v1/recording"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Recording{}, fmt.Errorf("create browser recording request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+config.Token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "video/webm, application/json")
	if rangeHeader != "" {
		httpRequest.Header.Set("Range", rangeHeader)
	}
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return Recording{}, fmt.Errorf("%w: could not connect", ErrUnavailable)
	}
	expectedStatus := http.StatusOK
	if rangeHeader != "" {
		expectedStatus = http.StatusPartialContent
	}
	if response.StatusCode != expectedStatus {
		defer response.Body.Close()
		contents, readErr := io.ReadAll(io.LimitReader(response.Body, maxConfigBytes+1))
		if readErr != nil || int64(len(contents)) > maxConfigBytes {
			return Recording{}, ErrUnavailable
		}
		var envelope providerResponse
		decoder := json.NewDecoder(bytes.NewReader(contents))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&envelope) != nil || requireJSONEOF(decoder) != nil || envelope.OK == nil || *envelope.OK || len(envelope.Result) != 0 {
			return Recording{}, errors.Join(ErrUnavailable, ErrInvalidResponse)
		}
		var failure providerError
		errorDecoder := json.NewDecoder(bytes.NewReader(envelope.Error))
		errorDecoder.DisallowUnknownFields()
		if errorDecoder.Decode(&failure) != nil || requireJSONEOF(errorDecoder) != nil {
			return Recording{}, errors.Join(ErrUnavailable, ErrInvalidResponse)
		}
		if response.StatusCode == http.StatusNotFound && failure.Code == "recording_not_found" {
			return Recording{}, ErrRecordingNotFound
		}
		if response.StatusCode == http.StatusRequestedRangeNotSatisfiable && failure.Code == "recording_range_not_satisfiable" {
			total, rangeErr := parseUnsatisfiedRecordingRange(response.Header.Get("Content-Range"))
			if rangeErr != nil {
				return Recording{}, errors.Join(ErrUnavailable, ErrInvalidResponse)
			}
			return Recording{}, &RecordingRangeError{Size: total}
		}
		if _, allowed := allowedOperationErrorCodes[failure.Code]; allowed {
			return Recording{}, &OperationError{Code: failure.Code}
		}
		return Recording{}, ErrProvider
	}

	contentType, parameters, typeErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	mimeType := "video/webm"
	if codecs, ok := parameters["codecs"]; ok && (codecs == "vp8" || codecs == "vp9") {
		mimeType += ";codecs=" + codecs
		delete(parameters, "codecs")
	}
	if typeErr != nil || contentType != "video/webm" || len(parameters) != 0 ||
		(rangeHeader != "" && !strings.EqualFold(response.Header.Get("Accept-Ranges"), "bytes")) ||
		(response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity") ||
		response.ContentLength <= 0 || response.ContentLength > MaxRecordingBytes {
		response.Body.Close()
		return Recording{}, errors.Join(ErrUnavailable, ErrInvalidResponse)
	}

	actual := RecordingRange{Start: 0, End: response.ContentLength - 1, Size: response.ContentLength, Total: response.ContentLength}
	if rangeHeader != "" {
		actual, err = parseRecordingContentRange(response.Header.Get("Content-Range"))
		if err == nil {
			expected, expectedErr := ResolveRecordingRange(rangeHeader, actual.Total)
			if expectedErr != nil || expected.Start != actual.Start || expected.End != actual.End || response.ContentLength != actual.Size {
				err = ErrInvalidResponse
			}
		}
		if err != nil {
			response.Body.Close()
			return Recording{}, errors.Join(ErrUnavailable, ErrInvalidResponse)
		}
	} else if response.Header.Get("Content-Range") != "" {
		response.Body.Close()
		return Recording{}, errors.Join(ErrUnavailable, ErrInvalidResponse)
	}
	title := ""
	if encodedTitle := response.Header.Get("X-Kiwi-Code-Recording-Title"); encodedTitle != "" {
		if decoded, decodeErr := url.QueryUnescape(encodedTitle); decodeErr == nil && len(decoded) <= 120 && !strings.ContainsRune(decoded, '\x00') {
			title = decoded
		}
	}
	return Recording{Body: response.Body, Size: actual.Size, TotalSize: actual.Total, Start: actual.Start, End: actual.End, Partial: rangeHeader != "", MIMEType: mimeType, Title: title}, nil
}
