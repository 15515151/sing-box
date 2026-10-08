package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/experimental/deprecated"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing/service"
)

func TestReloadServiceSamePort(t *testing.T) {
	prepareReloadTestContext(t)
	port := unusedReloadTestPort(t)
	previous := decodeReloadTestEntries(t, reloadTestConfig(port, "previous", "direct"))
	instance, cancel := startReloadTestService(t, previous)
	t.Cleanup(func() { closeReloadTestService(t, instance, cancel) })
	assertReloadTestProxy(t, port)

	entries := decodeReloadTestEntries(t, reloadTestConfig(port, "updated", "direct"))
	previousInstance := instance
	var err error
	instance, cancel, err = reloadService(instance, cancel, entries, previous, make(chan os.Signal, 1))
	if err != nil {
		t.Fatal(err)
	}
	if instance == nil || instance == previousInstance {
		t.Fatal("reload did not replace the service")
	}
	assertReloadTestInbound(t, instance, "updated", "previous")
	assertReloadTestProxy(t, port)
}

func TestReloadServiceConstructorFailureKeepsInstance(t *testing.T) {
	prepareReloadTestContext(t)
	port := unusedReloadTestPort(t)
	previous := decodeReloadTestEntries(t, reloadTestConfig(port, "previous", "direct"))
	instance, cancel := startReloadTestService(t, previous)
	t.Cleanup(func() { closeReloadTestService(t, instance, cancel) })

	invalidConfig := strings.Replace(reloadTestConfig(port, "invalid", "direct"),
		`{"type":"direct","tag":"direct"}`,
		`{"type":"selector","tag":"direct","outbounds":[]}`, 1)
	entries := decodeReloadTestEntries(t, invalidConfig)
	previousInstance := instance
	var err error
	instance, cancel, err = reloadService(instance, cancel, entries, previous, make(chan os.Signal, 1))
	if err == nil || !strings.Contains(err.Error(), "missing tags") {
		t.Fatalf("expected constructor failure, got %v", err)
	}
	if instance != previousInstance {
		t.Fatal("constructor failure replaced the running service")
	}
	assertReloadTestInbound(t, instance, "previous", "invalid")
	assertReloadTestProxy(t, port)
}

func TestReloadServiceStartFailureRestoresPrevious(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		missingFinal bool
	}{
		{name: "missing_final", missingFinal: true},
		{name: "occupied_port"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			prepareReloadTestContext(t)
			port := unusedReloadTestPort(t)
			previous := decodeReloadTestEntries(t, reloadTestConfig(port, "previous", "direct"))
			instance, cancel := startReloadTestService(t, previous)
			t.Cleanup(func() { closeReloadTestService(t, instance, cancel) })
			previousInstance := instance
			assertReloadTestProxy(t, port)

			candidatePort := port
			final := "missing"
			if !testCase.missingFinal {
				occupied, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer occupied.Close()
				candidatePort = occupied.Addr().(*net.TCPAddr).Port
				final = "direct"
			}
			entries := decodeReloadTestEntries(t, reloadTestConfig(candidatePort, "invalid", final))
			if testCase.missingFinal {
				previous[0].options.Route.Final = "mutated"
				previous[0].options.Inbounds[0].Tag = "mutated"
			}
			var err error
			instance, cancel, err = reloadService(instance, cancel, entries, previous, make(chan os.Signal, 1))
			if err == nil || !strings.Contains(err.Error(), "previous configuration restored") {
				t.Fatalf("expected successful rollback after start failure, got %v", err)
			}
			if instance == nil || instance == previousInstance {
				t.Fatal("start failure did not rebuild the previous service")
			}
			if testCase.missingFinal && !strings.Contains(err.Error(), "default outbound not found: missing") {
				t.Fatalf("unexpected start failure: %v", err)
			}
			assertReloadTestInbound(t, instance, "previous", "invalid")
			assertReloadTestProxy(t, port)

			validEntries := decodeReloadTestEntries(t, reloadTestConfig(port, "recovered", "direct"))
			instance, cancel, err = reloadService(instance, cancel, validEntries, previous, make(chan os.Signal, 1))
			if err != nil {
				t.Fatalf("reload after rollback: %v", err)
			}
			assertReloadTestInbound(t, instance, "recovered", "previous")
			assertReloadTestProxy(t, port)
		})
	}
}

func TestReloadServiceStopsWithoutRollbackOnPendingInterrupt(t *testing.T) {
	prepareReloadTestContext(t)
	port := unusedReloadTestPort(t)
	previous := decodeReloadTestEntries(t, reloadTestConfig(port, "previous", "direct"))
	instance, cancel := startReloadTestService(t, previous)
	t.Cleanup(func() { closeReloadTestService(t, instance, cancel) })
	entries := decodeReloadTestEntries(t, reloadTestConfig(port, "invalid", "missing"))
	osSignals := make(chan os.Signal, 1)
	osSignals <- os.Interrupt

	reloaded, reloadedCancel, err := reloadService(instance, cancel, entries, previous, osSignals)
	if err != nil || reloaded != nil || reloadedCancel != nil {
		t.Fatalf("pending interrupt did not stop the service: instance %v, cancel present %v, error %v", reloaded, reloadedCancel != nil, err)
	}
	connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err == nil {
		connection.Close()
		t.Fatal("previous listener was restored despite the pending interrupt")
	}
}

func TestReloadServiceMergesConfigurationEntries(t *testing.T) {
	prepareReloadTestContext(t)
	port := unusedReloadTestPort(t)
	previous := decodeReloadTestEntries(t,
		fmt.Sprintf(`{"log":{"disabled":true},"inbounds":[{"type":"mixed","tag":"previous","listen":"127.0.0.1","listen_port":%d}]}`, port),
		`{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`)
	instance, cancel := startReloadTestService(t, previous)
	t.Cleanup(func() { closeReloadTestService(t, instance, cancel) })

	entries := decodeReloadTestEntries(t,
		strings.Replace(string(previous[0].content), "previous", "updated", 1),
		string(previous[1].content))
	var err error
	instance, cancel, err = reloadService(instance, cancel, entries, previous, make(chan os.Signal, 1))
	if err != nil {
		t.Fatal(err)
	}
	assertReloadTestInbound(t, instance, "updated", "previous")
	assertReloadTestProxy(t, port)
}

func TestReloadServiceReusesCachedStdin(t *testing.T) {
	prepareReloadTestContext(t)
	port := unusedReloadTestPort(t)
	configPath := filepath.Join(t.TempDir(), "inbound.json")
	writeInbound := func(tag string) {
		t.Helper()
		content := fmt.Sprintf(`{"inbounds":[{"type":"mixed","tag":%q,"listen":"127.0.0.1","listen_port":%d}]}`, tag, port)
		if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeInbound("previous")
	stdinEntry := &OptionsEntry{
		path:    "stdin",
		content: []byte(`{"log":{"disabled":true},"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`),
	}
	paths := []string{configPath, "stdin"}
	previous, err := readConfigContents(paths, nil, stdinEntry)
	if err != nil {
		t.Fatal(err)
	}
	previous, err = decodeConfigs(previous)
	if err != nil {
		t.Fatal(err)
	}
	instance, cancel := startReloadTestService(t, previous)
	t.Cleanup(func() { closeReloadTestService(t, instance, cancel) })

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	writer.Close()
	previousStdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = previousStdin })
	writeInbound("updated")
	entries, err := readConfigContents(paths, nil, stdinEntry)
	if err != nil {
		t.Fatalf("reload read the consumed stdin: %v", err)
	}
	for _, entry := range entries {
		if entry.path == "stdin" && !bytes.Equal(entry.content, stdinEntry.content) {
			t.Fatal("cached stdin content changed")
		}
	}
	entries, err = decodeConfigs(entries)
	if err != nil {
		t.Fatal(err)
	}
	instance, cancel, err = reloadService(instance, cancel, entries, previous, make(chan os.Signal, 1))
	if err != nil {
		t.Fatal(err)
	}
	assertReloadTestInbound(t, instance, "updated", "previous")
	assertReloadTestProxy(t, port)
}

func prepareReloadTestContext(t *testing.T) {
	t.Helper()
	previousContext := globalCtx
	previousDisableColor := disableColor
	globalCtx = include.Context(service.ContextWith(context.Background(), deprecated.NewStderrManager(log.StdLogger())))
	disableColor = true
	t.Cleanup(func() {
		globalCtx = previousContext
		disableColor = previousDisableColor
	})
}

func reloadTestConfig(port int, tag, final string) string {
	return fmt.Sprintf(`{"log":{"disabled":true},"inbounds":[{"type":"mixed","tag":%q,"listen":"127.0.0.1","listen_port":%d}],"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":%q}}`, tag, port, final)
}

func decodeReloadTestEntries(t *testing.T, contents ...string) []*OptionsEntry {
	t.Helper()
	entries := make([]*OptionsEntry, 0, len(contents))
	for index, content := range contents {
		entry, err := decodeConfig(&OptionsEntry{content: []byte(content), path: fmt.Sprintf("config-%02d.json", index)})
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func startReloadTestService(t *testing.T, entries []*OptionsEntry) (*box.Box, context.CancelFunc) {
	t.Helper()
	options, err := mergeOptionsList(entries)
	if err != nil {
		t.Fatal(err)
	}
	instance, cancel, err := create(options)
	if err != nil {
		t.Fatal(err)
	}
	return instance, cancel
}

func closeReloadTestService(t *testing.T, instance *box.Box, cancel context.CancelFunc) {
	t.Helper()
	if instance != nil {
		if err := closeService(instance, cancel); err != nil {
			t.Errorf("close service: %v", err)
		}
	}
}

func unusedReloadTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func assertReloadTestInbound(t *testing.T, instance *box.Box, expected, removed string) {
	t.Helper()
	if instance == nil {
		t.Fatal("service is nil")
	}
	if _, exists := instance.Inbound().Get(expected); !exists {
		t.Fatalf("expected inbound %q is missing", expected)
	}
	if _, exists := instance.Inbound().Get(removed); exists {
		t.Fatalf("unexpected inbound %q remains", removed)
	}
}

func assertReloadTestProxy(t *testing.T, port int) {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, "proxy connection works")
	}))
	defer target.Close()
	proxyURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("request through proxy at %s: %v", proxyURL, err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(content) != "proxy connection works" {
		t.Fatalf("unexpected proxy response: status %d, body %q", response.StatusCode, content)
	}
}
