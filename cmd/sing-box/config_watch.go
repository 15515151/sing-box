package main

import (
	"bytes"
	"context"
	"time"
)

const configWatchInterval = 500 * time.Millisecond

type configChange struct {
	entries []*OptionsEntry
	err     error
}

func sameConfigContents(left, right []*OptionsEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for index, entry := range left {
		if entry.path != right[index].path || !bytes.Equal(entry.content, right[index].content) {
			return false
		}
	}
	return true
}

func watchConfig(ctx context.Context, paths, directories []string, stdinEntry *OptionsEntry, initial []*OptionsEntry) <-chan configChange {
	var hasFiles bool
	for _, path := range paths {
		if path != "stdin" {
			hasFiles = true
			break
		}
	}
	if !hasFiles && len(directories) == 0 {
		return nil
	}
	changes := make(chan configChange, 1)
	go func() {
		defer close(changes)
		ticker := time.NewTicker(configWatchInterval)
		defer ticker.Stop()
		lastEntries := initial
		var lastError string
		pendingEntries := initial
		var pendingError string
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			entries, err := readConfigContents(paths, directories, stdinEntry)
			var readError string
			if err != nil {
				readError = err.Error()
			}
			// Debounce saves by requiring two identical snapshots before notifying the service.
			stable := readError == pendingError && sameConfigContents(entries, pendingEntries)
			pendingEntries, pendingError = entries, readError
			if !stable || readError == lastError && sameConfigContents(entries, lastEntries) {
				continue
			}
			lastEntries, lastError = entries, readError
			change := configChange{entries: entries, err: err}
			select {
			case changes <- change:
			case <-ctx.Done():
				return
			default:
				// Keep the latest snapshot when the service is still processing a reload.
				select {
				case <-changes:
				default:
				}
				select {
				case changes <- change:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return changes
}
