package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const reloadTestHelperEnvironment = "SING_BOX_CONFIG_RELOAD_TEST_HELPER"

func TestRunReloadHelperProcess(t *testing.T) {
	if os.Getenv(reloadTestHelperEnvironment) != "1" {
		return
	}
	for index, argument := range os.Args {
		if argument == "--" {
			mainCommand.SetArgs(os.Args[index+1:])
			if err := mainCommand.Execute(); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("missing CLI arguments")
}

func TestRunAutomaticallyReloadsConfiguration(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	previousPort := unusedReloadTestPort(t)
	writeRunReloadTestConfig(t, configPath, reloadTestConfig(previousPort, "previous", "direct"))
	outputPath := startRunReloadTestProcess(t, "run", "-c", configPath)
	waitRunReloadTest(t, outputPath, "initial listener", func() bool {
		return runReloadTestPortOpen(previousPort)
	})
	assertReloadTestProxy(t, previousPort)

	updatedPort := unusedReloadTestPort(t)
	writeRunReloadTestConfig(t, configPath, reloadTestConfig(updatedPort, "updated", "direct"))
	waitRunReloadTest(t, outputPath, "updated listener", func() bool {
		return runReloadTestPortOpen(updatedPort) && !runReloadTestPortOpen(previousPort)
	})
	assertReloadTestProxy(t, updatedPort)

	writeRunReloadTestConfig(t, configPath, `{"inbounds":`)
	waitRunReloadTest(t, outputPath, "invalid configuration rejection", func() bool {
		output, _ := os.ReadFile(outputPath)
		return strings.Contains(string(output), "decode config at")
	})
	assertReloadTestProxy(t, updatedPort)

	recoveredPort := unusedReloadTestPort(t)
	writeRunReloadTestConfig(t, configPath, reloadTestConfig(recoveredPort, "recovered", "direct"))
	waitRunReloadTest(t, outputPath, "listener after invalid configuration", func() bool {
		return runReloadTestPortOpen(recoveredPort) && !runReloadTestPortOpen(updatedPort)
	})
	assertReloadTestProxy(t, recoveredPort)
}

func TestRunCanDisableAutomaticReload(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	previousPort := unusedReloadTestPort(t)
	writeRunReloadTestConfig(t, configPath, reloadTestConfig(previousPort, "previous", "direct"))
	outputPath := startRunReloadTestProcess(t, "run", "--watch=false", "-c", configPath)
	waitRunReloadTest(t, outputPath, "initial listener", func() bool {
		return runReloadTestPortOpen(previousPort)
	})
	updatedPort := unusedReloadTestPort(t)
	writeRunReloadTestConfig(t, configPath, reloadTestConfig(updatedPort, "updated", "direct"))
	deadline := time.Now().Add(4 * configWatchInterval)
	for time.Now().Before(deadline) {
		if runReloadTestPortOpen(updatedPort) || !runReloadTestPortOpen(previousPort) {
			t.Fatal("configuration was automatically reloaded with --watch=false")
		}
		time.Sleep(25 * time.Millisecond)
	}
	assertReloadTestProxy(t, previousPort)
}

func startRunReloadTestProcess(t *testing.T, arguments ...string) string {
	t.Helper()
	outputPath := filepath.Join(t.TempDir(), "sing-box.log")
	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	commandArguments := append([]string{"-test.run=^TestRunReloadHelperProcess$", "--"}, arguments...)
	command := exec.Command(os.Args[0], commandArguments...)
	command.Env = append(os.Environ(), reloadTestHelperEnvironment+"=1")
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		output.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		_ = output.Close()
		if t.Failed() {
			content, _ := os.ReadFile(outputPath)
			t.Logf("sing-box output:\n%s", content)
		}
	})
	return outputPath
}

func writeRunReloadTestConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitRunReloadTest(t *testing.T, outputPath, description string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	output, _ := os.ReadFile(outputPath)
	t.Fatalf("timed out waiting for %s:\n%s", description, output)
}

func runReloadTestPortOpen(port int) bool {
	connection, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
	if err != nil {
		return false
	}
	connection.Close()
	return true
}
