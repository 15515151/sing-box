package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeWatchedConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func startConfigWatch(t *testing.T, paths, directories []string, initial []*OptionsEntry) (<-chan configChange, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return watchConfig(ctx, paths, directories, nil, initial), cancel
}

func awaitConfigChange(t *testing.T, changes <-chan configChange) configChange {
	t.Helper()
	select {
	case change, open := <-changes:
		if !open {
			t.Fatal("configuration watcher stopped unexpectedly")
		}
		return change
	case <-time.After(6 * configWatchInterval):
		t.Fatal("configuration change was not detected")
	}
	return configChange{}
}

func assertNoConfigChange(t *testing.T, changes <-chan configChange) {
	t.Helper()
	select {
	case <-changes:
		t.Fatal("unexpected configuration change")
	case <-time.After(3 * configWatchInterval):
	}
}

func TestConfigWatchInitialSnapshotAndAtomicReplacement(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	initial := []*OptionsEntry{{path: path, content: []byte(`{}`)}}
	// A change between the initial read and watcher startup must still be detected.
	writeWatchedConfig(t, path, `{"log":{"level":"debug"}}`)
	changes, _ := startConfigWatch(t, []string{path}, nil, initial)
	awaitConfigChange(t, changes)
	assertNoConfigChange(t, changes)

	replacement := filepath.Join(directory, "replacement.tmp")
	writeWatchedConfig(t, replacement, `{"log":{"level":"warn"}}`)
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	awaitConfigChange(t, changes)
	// Replacing the file with identical contents must not interrupt connections.
	writeWatchedConfig(t, replacement, `{"log":{"level":"warn"}}`)
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	assertNoConfigChange(t, changes)
}

func TestConfigWatchDirectoryChanges(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	directory := filepath.Join(parent, "config.d")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	changes, _ := startConfigWatch(t, nil, []string{directory}, nil)
	writeWatchedConfig(t, filepath.Join(directory, "notes.txt"), "ignored")
	assertNoConfigChange(t, changes)
	path := filepath.Join(directory, "route.json")
	writeWatchedConfig(t, path, `{}`)
	awaitConfigChange(t, changes)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	awaitConfigChange(t, changes)

	// Watching contents also survives replacing the entire configuration directory.
	replacement := filepath.Join(parent, "replacement")
	if err := os.Mkdir(replacement, 0o700); err != nil {
		t.Fatal(err)
	}
	writeWatchedConfig(t, filepath.Join(replacement, "new.json"), `{}`)
	if err := os.Rename(directory, filepath.Join(parent, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, directory); err != nil {
		t.Fatal(err)
	}
	awaitConfigChange(t, changes)
}

func TestConfigWatchReadErrorAndRecovery(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	writeWatchedConfig(t, path, `{}`)
	initial := []*OptionsEntry{{path: path, content: []byte(`{}`)}}
	changes, _ := startConfigWatch(t, []string{path}, nil, initial)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	awaitConfigChange(t, changes)
	assertNoConfigChange(t, changes)
	writeWatchedConfig(t, path, `{}`)
	awaitConfigChange(t, changes)
	// Invalid JSON is a changed snapshot too; the run loop handles validation.
	writeWatchedConfig(t, path, `{"inbounds":`)
	awaitConfigChange(t, changes)
	assertNoConfigChange(t, changes)
}

func TestConfigWatchWaitsForStableContents(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	writeWatchedConfig(t, path, `{}`)
	changes, _ := startConfigWatch(t, []string{path}, nil, []*OptionsEntry{{path: path, content: []byte(`{}`)}})
	writeWatchedConfig(t, path, `{"inbounds":`)
	// The first sample alone must not trigger a reload.
	select {
	case <-changes:
		t.Fatal("reloaded before contents settled")
	case <-time.After(configWatchInterval + configWatchInterval/2):
	}
	writeWatchedConfig(t, path, `{"inbounds":[]}`)
	change := awaitConfigChange(t, changes)
	if change.err != nil || len(change.entries) != 1 || string(change.entries[0].content) != `{"inbounds":[]}` {
		t.Fatalf("unexpected settled snapshot: %+v", change)
	}
	assertNoConfigChange(t, changes)
}

func TestConfigWatchKeepsLatestSnapshot(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	writeWatchedConfig(t, path, `{}`)
	changes, _ := startConfigWatch(t, []string{path}, nil, []*OptionsEntry{{path: path, content: []byte(`{}`)}})
	writeWatchedConfig(t, path, `{"log":{"level":"debug"}}`)
	time.Sleep(3 * configWatchInterval)
	writeWatchedConfig(t, path, `{"log":{"level":"warn"}}`)
	time.Sleep(3 * configWatchInterval)
	change := awaitConfigChange(t, changes)
	if change.err != nil || len(change.entries) != 1 || string(change.entries[0].content) != `{"log":{"level":"warn"}}` {
		t.Fatalf("pending change did not contain the latest snapshot: %+v", change)
	}
	// The notification owns its contents even if the file changes before validation.
	writeWatchedConfig(t, path, `{"inbounds":`)
	if string(change.entries[0].content) != `{"log":{"level":"warn"}}` {
		t.Fatal("notified contents changed with the file")
	}
}

func TestConfigWatchStopsOnCancellation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	writeWatchedConfig(t, path, `{}`)
	changes, cancel := startConfigWatch(t, []string{path}, nil, []*OptionsEntry{{path: path, content: []byte(`{}`)}})
	cancel()
	select {
	case _, open := <-changes:
		if open {
			t.Fatal("watcher emitted a change after cancellation")
		}
	case <-time.After(2 * configWatchInterval):
		t.Fatal("watcher did not stop")
	}
}

func TestConfigWatchSkipsStandardInputOnly(t *testing.T) {
	t.Parallel()
	stdinEntry := &OptionsEntry{path: "stdin", content: []byte(`{}`)}
	changes := watchConfig(context.Background(), []string{"stdin"}, nil, stdinEntry, []*OptionsEntry{stdinEntry})
	if changes != nil {
		t.Fatal("standard input cannot be watched")
	}
}
