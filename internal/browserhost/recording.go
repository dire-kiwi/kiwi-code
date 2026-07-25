package browserhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dire-kiwi/kiwi-code/internal/browsercontrol"
)

const maxRecordingMetadataBytes = 16 << 10

var recordingIDPattern = regexp.MustCompile(`^rec-[a-f0-9]{32}$`)

type persistedRecording struct {
	Version    int    `json:"version"`
	ID         string `json:"id"`
	ProjectID  string `json:"projectId"`
	ThreadID   string `json:"threadId"`
	TargetID   string `json:"targetId"`
	Title      string `json:"title"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	DurationMS int64  `json:"durationMs"`
	Bytes      int64  `json:"bytes"`
	MIMEType   string `json:"mimeType"`
	Filename   string `json:"filename"`
}

type limitedRecordingBody struct {
	io.Reader
	file *os.File
}

func (body *limitedRecordingBody) Close() error { return body.file.Close() }

func (p *Provider) OpenRecordingRange(_ context.Context, projectID, threadID, recordingID, rangeHeader string) (browsercontrol.Recording, error) {
	if !recordingIDPattern.MatchString(recordingID) || p.recordingsDir == "" {
		return browsercontrol.Recording{}, browsercontrol.ErrRecordingNotFound
	}
	metadataPath := filepath.Join(p.recordingsDir, recordingID+".json")
	metadataFile, err := os.Open(metadataPath)
	if err != nil {
		return browsercontrol.Recording{}, browsercontrol.ErrRecordingNotFound
	}
	metadataBytes, readErr := io.ReadAll(io.LimitReader(metadataFile, maxRecordingMetadataBytes+1))
	closeErr := metadataFile.Close()
	if readErr != nil || closeErr != nil || len(metadataBytes) > maxRecordingMetadataBytes {
		return browsercontrol.Recording{}, browsercontrol.ErrRecordingNotFound
	}
	var metadata persistedRecording
	decoder := json.NewDecoder(bytes.NewReader(metadataBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&metadata) != nil || requireRecordingJSONEOF(decoder) != nil ||
		metadata.Version != 1 || metadata.ID != recordingID || metadata.ProjectID != projectID || metadata.ThreadID != threadID ||
		metadata.Filename != recordingID+".webm" || metadata.Bytes <= 0 || metadata.Bytes > browsercontrol.MaxRecordingBytes ||
		len(metadata.Title) < 1 || len(metadata.Title) > 120 || strings.ContainsRune(metadata.Title, '\x00') ||
		(metadata.MIMEType != "video/webm" && metadata.MIMEType != "video/webm;codecs=vp8" && metadata.MIMEType != "video/webm;codecs=vp9") {
		return browsercontrol.Recording{}, browsercontrol.ErrRecordingNotFound
	}
	if _, err := time.Parse(time.RFC3339, metadata.StartedAt); err != nil {
		return browsercontrol.Recording{}, browsercontrol.ErrRecordingNotFound
	}
	if _, err := time.Parse(time.RFC3339, metadata.FinishedAt); err != nil {
		return browsercontrol.Recording{}, browsercontrol.ErrRecordingNotFound
	}
	videoPath := filepath.Join(p.recordingsDir, metadata.Filename)
	info, err := os.Lstat(videoPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != metadata.Bytes {
		return browsercontrol.Recording{}, browsercontrol.ErrRecordingNotFound
	}
	file, err := os.Open(videoPath)
	if err != nil {
		return browsercontrol.Recording{}, browsercontrol.ErrRecordingNotFound
	}
	fileInfo, err := file.Stat()
	if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Size() != metadata.Bytes {
		_ = file.Close()
		return browsercontrol.Recording{}, browsercontrol.ErrRecordingNotFound
	}
	rangeValue, err := browsercontrol.ResolveRecordingRange(rangeHeader, metadata.Bytes)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, browsercontrol.ErrRecordingRangeNotSatisfiable) {
			return browsercontrol.Recording{}, &browsercontrol.RecordingRangeError{Size: metadata.Bytes}
		}
		return browsercontrol.Recording{}, err
	}
	if _, err := file.Seek(rangeValue.Start, io.SeekStart); err != nil {
		_ = file.Close()
		return browsercontrol.Recording{}, browsercontrol.ErrRecordingNotFound
	}
	return browsercontrol.Recording{
		Body:      &limitedRecordingBody{Reader: io.LimitReader(file, rangeValue.Size), file: file},
		Size:      rangeValue.Size,
		TotalSize: rangeValue.Total,
		Start:     rangeValue.Start,
		End:       rangeValue.End,
		Partial:   strings.TrimSpace(rangeHeader) != "",
		MIMEType:  metadata.MIMEType,
		Title:     metadata.Title,
	}, nil
}

func requireRecordingJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return browsercontrol.ErrInvalidResponse
	}
	return nil
}
