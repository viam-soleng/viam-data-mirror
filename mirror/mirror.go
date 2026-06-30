// Package mirror implements the viam-soleng:data:mirror generic service, which
// periodically syncs binary data from Viam's data management down to local files.
package mirror

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"go.viam.com/rdk/app"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/services/generic"
)

// Model is the full triplet for this module's resource.
var Model = resource.NewModel("viam-soleng", "data", "mirror")

const (
	defaultSyncFrequency = 60 * time.Second
	connectionRetries    = 3

	// Preserve 5% of the filesystem. Do not allow this module to fill the drive.
	minFreeFraction = 0.05
)

func init() {
	resource.RegisterService(generic.API, Model, resource.Registration[resource.Resource, *Config]{
		Constructor: newMirror,
	})
}

// Config describes the JSON configuration for the mirror service.
type Config struct {
	AppAPIKey     string   `json:"app_api_key"`
	AppAPIKeyID   string   `json:"app_api_key_id"`
	SyncFrequency float64  `json:"sync_frequency"`
	Tags          []string `json:"tags"`
	Labels        []string `json:"labels"`
	DatasetID     string   `json:"dataset_id"`
	MirrorPath    string   `json:"mirror_path"`
	Delete        bool     `json:"delete"`
	ProtectedDirs []string `json:"protected_dirs"`
}

// Validate ensures the required attributes are present. It returns no dependencies.
func (cfg *Config) Validate(path string) ([]string, []string, error) {
	if cfg.AppAPIKey == "" {
		return nil, nil, resource.NewConfigValidationFieldRequiredError(path, "app_api_key")
	}
	if cfg.AppAPIKeyID == "" {
		return nil, nil, resource.NewConfigValidationFieldRequiredError(path, "app_api_key_id")
	}
	return nil, nil, nil
}

type mirror struct {
	resource.Named
	resource.AlwaysRebuild

	logger logging.Logger

	apiKey        string
	apiKeyID      string
	tags          []string
	labels        []string
	datasetID     string
	mirrorPath    string
	delete        bool
	protectedDirs []string
	syncFrequency time.Duration

	client *app.ViamClient

	workers sync.WaitGroup
	cancel  context.CancelFunc
}

func newMirror(
	ctx context.Context, deps resource.Dependencies, conf resource.Config, logger logging.Logger,
) (resource.Resource, error) {
	m := &mirror{
		Named:  conf.ResourceName().AsNamed(),
		logger: logger,
	}
	if err := m.reconfigure(conf); err != nil {
		return nil, err
	}

	cancelCtx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.workers.Add(1)
	go m.syncLoop(cancelCtx)

	return m, nil
}

// reconfigure applies a parsed config to the receiver. It does not start or stop
// the sync loop; that lifecycle is owned by the constructor and Close.
func (m *mirror) reconfigure(conf resource.Config) error {
	cfg, err := resource.NativeConfig[*Config](conf)
	if err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("could not determine home directory: %w", err)
	}

	mirrorPath := filepath.Join(home, ".viam", "data_mirror")
	if cfg.MirrorPath != "" {
		mirrorPath = filepath.Join(home, ".viam", cfg.MirrorPath)
	}

	syncFrequency := defaultSyncFrequency
	if cfg.SyncFrequency > 0 {
		syncFrequency = time.Duration(cfg.SyncFrequency * float64(time.Second))
	}

	m.apiKey = cfg.AppAPIKey
	m.apiKeyID = cfg.AppAPIKeyID
	m.tags = cfg.Tags
	m.labels = cfg.Labels
	m.datasetID = cfg.DatasetID
	m.mirrorPath = mirrorPath
	m.delete = cfg.Delete
	m.protectedDirs = cfg.ProtectedDirs
	m.syncFrequency = syncFrequency

	return nil
}

// DoCommand is unused but required to satisfy the generic service API.
func (m *mirror) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

// Close stops the sync loop and tears down the connection.
func (m *mirror) Close(ctx context.Context) error {
	if m.cancel != nil {
		m.cancel()
	}
	m.workers.Wait()
	m.closeConnection()
	return nil
}

// syncLoop runs do_sync on the configured interval, reconnecting with exponential
// backoff when an error occurs.
func (m *mirror) syncLoop(ctx context.Context) {
	defer m.workers.Done()
	m.logger.Info("Starting sync loop")

	consecutiveFailures := 0
	for {
		if ctx.Err() != nil {
			m.logger.Info("Exiting sync loop")
			return
		}

		if err := m.ensureConnection(ctx); err != nil {
			m.logger.Errorf("Failed to establish connection: %s. Retrying...", err)
			if !sleep(ctx, 5*time.Second) {
				return
			}
			continue
		}

		if err := m.doSync(ctx); err != nil {
			consecutiveFailures++
			m.logger.Errorf("Error in sync loop (attempt %d): %s", consecutiveFailures, err)

			// Force reconnection on error.
			m.closeConnection()

			// Exponential backoff, capped at 60 seconds.
			delay := time.Duration(1<<(consecutiveFailures-1)) * time.Second
			if delay > 60*time.Second {
				delay = 60 * time.Second
			}
			m.logger.Infof("Retrying in %s...", delay)
			if !sleep(ctx, delay) {
				return
			}
			continue
		}

		consecutiveFailures = 0
		if !sleep(ctx, m.syncFrequency) {
			return
		}
	}
}

// sleep waits for d or until the context is cancelled. It returns false if the
// context was cancelled.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// ensureConnection makes sure m.client is connected, retrying on failure.
func (m *mirror) ensureConnection(ctx context.Context) error {
	if m.client != nil {
		return nil
	}

	var lastErr error
	for attempt := 0; attempt < connectionRetries; attempt++ {
		m.logger.Infof("Attempting to connect (attempt %d/%d)...", attempt+1, connectionRetries)
		client, err := app.CreateViamClientWithAPIKey(ctx, app.Options{}, m.apiKey, m.apiKeyID, m.logger)
		if err == nil {
			m.client = client
			m.logger.Info("Connection established.")
			return nil
		}
		lastErr = err
		m.logger.Errorf("Connection attempt %d failed: %s", attempt+1, err)
		if attempt < connectionRetries-1 {
			if !sleep(ctx, time.Second) {
				return ctx.Err()
			}
		}
	}
	return fmt.Errorf("failed to connect after %d attempts: %w", connectionRetries, lastErr)
}

// closeConnection closes and clears the active ViamClient, if any.
func (m *mirror) closeConnection() {
	if m.client != nil {
		if err := m.client.Close(); err != nil {
			m.logger.Errorf("Error closing connection: %s", err)
		} else {
			m.logger.Info("Connection closed.")
		}
		m.client = nil
	}
}

// doSync pages through binary data matching the filter, writes any files missing
// from the mirror directory, and (if delete is enabled) removes extra files.
func (m *mirror) doSync(ctx context.Context) error {
	client := m.client
	if client == nil {
		return errors.New("no active connection")
	}
	data := client.DataClient()

	// Build the set of files currently on disk so we can detect extras to remove.
	// A value of true means "not yet seen in data management" (candidate for deletion).
	currentFiles := map[string]bool{}
	if err := filepath.Walk(m.mirrorPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.IsDir() {
			currentFiles[path] = true
		}
		return nil
	}); err != nil {
		return fmt.Errorf("error walking mirror path: %w", err)
	}

	filter := &app.Filter{}
	if m.datasetID != "" {
		filter.DatasetID = m.datasetID
	}
	if len(m.tags) > 0 {
		filter.TagsFilter = app.TagsFilter{Tags: m.tags}
	}
	if len(m.labels) > 0 {
		filter.BboxLabels = m.labels
	}

	// spaceExhausted is set when a download would breach the free-space reserve.
	// It stops the cycle early and suppresses the delete pass, since currentFiles
	// is then incomplete and cannot be trusted to identify extras.
	spaceExhausted := false

	last := ""
pagingLoop:
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		resp, err := data.BinaryDataByFilter(ctx, false, &app.DataByFilterOptions{
			Filter: filter,
			Last:   last,
		})
		if err != nil {
			return fmt.Errorf("error fetching binary data by filter: %w", err)
		}
		if len(resp.BinaryData) == 0 {
			break
		}

		for _, b := range resp.BinaryData {
			if b.Metadata == nil {
				m.logger.Warn("binary data with nil metadata, skipping")
				continue
			}
			file := filepath.Join(m.mirrorPath, fileNameFor(b.Metadata))

			// If the file already exists, assume it is a match and do not re-write it.
			if _, statErr := os.Stat(file); statErr == nil {
				m.logger.Debugf("%s exists already, skipping", file)
				currentFiles[file] = false
				continue
			}

			id := b.Metadata.BinaryDataID
			if id == "" {
				id = b.Metadata.ID
			}
			withData, err := data.BinaryDataByIDs(ctx, []string{id})
			if err != nil {
				return fmt.Errorf("error fetching binary data for %s: %w", id, err)
			}
			if len(withData) == 0 {
				m.logger.Warnf("no binary data returned for %s, skipping", id)
				continue
			}

			content := withData[0].Binary
			room, err := m.hasRoomFor(int64(len(content)))
			if err != nil {
				return fmt.Errorf("failed to check free space on %s before writing %s: %w", m.mirrorPath, file, err)
			}
			if !room {
				if m.fileCanNeverFit(int64(len(content))) {
					m.logger.Errorf("%s (%d bytes) can never fit while preserving %.0f%% free space; skipping",
						file, len(content), minFreeFraction*100)
					continue
				}
				m.logger.Warnf("Pausing sync: writing %s (%d bytes) would drop free space below %.0f%%",
					file, len(content), minFreeFraction*100)
				spaceExhausted = true
				break pagingLoop
			}

			m.writeFile(file, content)
			currentFiles[file] = false
		}

		last = resp.Last
	}

	if m.delete && !spaceExhausted {
		for file, extra := range currentFiles {
			if extra {
				if err := os.Remove(file); err != nil {
					m.logger.Errorf("Error deleting %s: %s", file, err)
				} else {
					m.logger.Infof("Deleted %s", file)
				}
			}
		}
		m.removeEmptyDirs()
	}

	return nil
}

// fileNameFor determines the relative path to write a binary datum to. It mirrors
// the Python module: use the file_name when present, otherwise the data ID plus an
// extension guessed from the MIME type.
func fileNameFor(meta *app.BinaryMetadata) string {
	if meta.FileName != "" {
		return meta.FileName
	}
	ext := meta.FileExt
	if ext == "" {
		if exts, err := mime.ExtensionsByType(meta.CaptureMetadata.MimeType); err == nil && len(exts) > 0 {
			ext = exts[0]
		}
	}
	return meta.ID + ext
}

// hasRoomFor reports whether writing nBytes would still leave at least
// minFreeFraction of total capacity free on the filesystem backing mirrorPath.
func (m *mirror) hasRoomFor(nBytes int64) (bool, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(m.mirrorPath, &st); err != nil {
		return false, err
	}
	bsize := int64(st.Bsize)
	total := int64(st.Blocks) * bsize
	avail := int64(st.Bavail) * bsize // space available to non-root users
	reserve := int64(float64(total) * minFreeFraction)
	return avail-nBytes >= reserve, nil
}

// fileCanNeverFit reports whether nBytes exceeds the usable capacity of the
// filesystem (total minus the reserve), i.e. it could not be written even on an
// otherwise empty disk. Such a file would otherwise pause every sync cycle.
func (m *mirror) fileCanNeverFit(nBytes int64) bool {
	var st syscall.Statfs_t
	if err := syscall.Statfs(m.mirrorPath, &st); err != nil {
		return false
	}
	total := int64(st.Blocks) * int64(st.Bsize)
	reserve := int64(float64(total) * minFreeFraction)
	return nBytes > total-reserve
}

// writeFile writes content to file_path, creating parent directories as needed.
func (m *mirror) writeFile(filePath string, content []byte) {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		m.logger.Errorf("An error occurred while creating directories for %s: %s", filePath, err)
		return
	}
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		m.logger.Errorf("An error occurred while writing the file %s: %s", filePath, err)
		return
	}
	m.logger.Infof("File successfully written: %s", filePath)
}

// removeEmptyDirs removes empty directories under mirrorPath from the bottom up,
// skipping the root and any protected directories.
func (m *mirror) removeEmptyDirs() {
	var dirs []string
	if err := filepath.Walk(m.mirrorPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	}); err != nil {
		m.logger.Errorf("Error walking directories for cleanup: %s", err)
		return
	}

	// filepath.Walk visits parents before children in lexical order, so iterating
	// in reverse processes children before parents (bottom-up), letting a parent
	// be removed in the same pass once its last child directory is gone.
	for i := len(dirs) - 1; i >= 0; i-- {
		dirpath := dirs[i]

		// Skip the root mirror directory.
		if dirpath == m.mirrorPath {
			continue
		}

		relativeDir, err := filepath.Rel(m.mirrorPath, dirpath)
		if err != nil {
			m.logger.Errorf("Error computing relative path for %s: %s", dirpath, err)
			continue
		}
		if m.isProtected(relativeDir) {
			m.logger.Debugf("Skipping deletion for protected directory: %s", dirpath)
			continue
		}

		entries, err := os.ReadDir(dirpath)
		if err != nil {
			m.logger.Errorf("Error reading directory %s: %s", dirpath, err)
			continue
		}
		if len(entries) == 0 {
			if err := os.Remove(dirpath); err != nil {
				m.logger.Errorf("Error removing directory %s: %s", dirpath, err)
			} else {
				m.logger.Infof("Removed empty directory: %s", dirpath)
			}
		}
	}
}

func (m *mirror) isProtected(relativeDir string) bool {
	return slices.Contains(m.protectedDirs, relativeDir)
}
