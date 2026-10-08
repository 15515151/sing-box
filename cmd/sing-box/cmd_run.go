package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	runtimeDebug "runtime/debug"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/sagernet/sing-box"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badjson"
	"github.com/sagernet/sing/service"

	"github.com/spf13/cobra"
)

var commandRun = &cobra.Command{
	Use:   "run",
	Short: "Run service",
	Run: func(cmd *cobra.Command, args []string) {
		err := run()
		if err != nil {
			log.Fatal(err)
		}
	},
}

func init() {
	mainCommand.AddCommand(commandRun)
	commandRun.Flags().BoolVar(&autoReload, "watch", true, "automatically reload changed configuration files")
}

var autoReload bool

type OptionsEntry struct {
	content []byte
	path    string
	options option.Options
}

func readConfigContentAt(path string) (*OptionsEntry, error) {
	var (
		configContent []byte
		err           error
	)
	if path == "stdin" {
		configContent, err = io.ReadAll(os.Stdin)
	} else {
		configContent, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, E.Cause(err, "read config at ", path)
	}
	return &OptionsEntry{content: configContent, path: path}, nil
}

func decodeConfig(entry *OptionsEntry) (*OptionsEntry, error) {
	options, err := json.UnmarshalExtendedContext[option.Options](globalCtx, entry.content)
	if err != nil {
		return nil, E.Cause(err, "decode config at ", entry.path)
	}
	return &OptionsEntry{
		content: entry.content,
		path:    entry.path,
		options: options,
	}, nil
}

func readConfig() ([]*OptionsEntry, error) {
	optionsList, err := readConfigContents(configPaths, configDirectories, nil)
	if err != nil {
		return nil, err
	}
	return decodeConfigs(optionsList)
}

func decodeConfigs(entries []*OptionsEntry) ([]*OptionsEntry, error) {
	optionsList := make([]*OptionsEntry, 0, len(entries))
	for _, entry := range entries {
		decoded, err := decodeConfig(entry)
		if err != nil {
			return nil, err
		}
		optionsList = append(optionsList, decoded)
	}
	return optionsList, nil
}

func readConfigContents(paths, directories []string, stdinEntry *OptionsEntry) ([]*OptionsEntry, error) {
	var optionsList []*OptionsEntry
	for _, path := range paths {
		var optionsEntry *OptionsEntry
		var err error
		if path == "stdin" && stdinEntry != nil {
			optionsEntry = &OptionsEntry{content: stdinEntry.content, path: path}
		} else {
			optionsEntry, err = readConfigContentAt(path)
			if err != nil {
				return nil, err
			}
		}
		if path == "stdin" {
			stdinEntry = optionsEntry
		}
		optionsList = append(optionsList, optionsEntry)
	}
	for _, directory := range directories {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return nil, E.Cause(err, "read config directory at ", directory)
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".json") || entry.IsDir() {
				continue
			}
			optionsEntry, err := readConfigContentAt(filepath.Join(directory, entry.Name()))
			if err != nil {
				return nil, err
			}
			optionsList = append(optionsList, optionsEntry)
		}
	}
	sort.Slice(optionsList, func(i, j int) bool {
		return optionsList[i].path < optionsList[j].path
	})
	return optionsList, nil
}

func readConfigAndMerge() (option.Options, error) {
	optionsList, err := readConfig()
	if err != nil {
		return option.Options{}, err
	}
	return mergeOptionsList(optionsList)
}

func mergeOptionsList(optionsList []*OptionsEntry) (option.Options, error) {
	if len(optionsList) == 1 {
		return optionsList[0].options, nil
	}
	var (
		mergedMessage json.RawMessage
		err           error
	)
	for _, options := range optionsList {
		mergedMessage, err = badjson.MergeJSON(globalCtx, options.options.RawMessage, mergedMessage, false)
		if err != nil {
			return option.Options{}, E.Cause(err, "merge config at ", options.path)
		}
	}
	var mergedOptions option.Options
	err = mergedOptions.UnmarshalJSONContext(globalCtx, mergedMessage)
	if err != nil {
		return option.Options{}, E.Cause(err, "unmarshal merged config")
	}
	return mergedOptions, nil
}

func create(options option.Options) (*box.Box, context.CancelFunc, error) {
	instance, cancel, err := newService(options)
	if err != nil {
		return nil, nil, err
	}
	err = startService(instance, cancel)
	if err != nil {
		return nil, nil, err
	}
	return instance, cancel, nil
}

func newService(options option.Options) (*box.Box, context.CancelFunc, error) {
	if disableColor {
		if options.Log == nil {
			options.Log = &option.LogOptions{}
		}
		options.Log.DisableColor = true
	}
	ctx, cancel := context.WithCancel(service.ExtendContext(globalCtx))
	instance, err := box.New(box.Options{
		Context:                    ctx,
		Options:                    options,
		NetworkNamespaceHolderArgs: []string{"/proc/self/exe", commandNetnsHolder.Use},
	})
	if err != nil {
		cancel()
		return nil, nil, E.Cause(err, "create service")
	}
	return instance, cancel, nil
}

func startService(instance *box.Box, cancel context.CancelFunc) error {
	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer func() {
		signal.Stop(osSignals)
		close(osSignals)
	}()
	startCtx, finishStart := context.WithCancel(context.Background())
	go func() {
		_, loaded := <-osSignals
		if loaded {
			cancel()
			closeMonitor(startCtx)
		}
	}()
	err := instance.Start()
	finishStart()
	if err != nil {
		cancel()
		return E.Cause(err, "start service")
	}
	return nil
}

func run() error {
	optionsList, err := readConfig()
	if err != nil {
		return err
	}
	options, err := mergeOptionsList(optionsList)
	if err != nil {
		return err
	}
	err = runInUserNamespaceIfNeeded(options, optionsList)
	if err != nil {
		return err
	}
	osSignals := make(chan os.Signal, 1)
	signal.Notify(osSignals, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(osSignals)
	var stdinEntry *OptionsEntry
	for _, entry := range optionsList {
		if entry.path == "stdin" {
			stdinEntry = entry
			break
		}
	}
	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	var configChanges <-chan configChange
	if autoReload {
		configChanges = watchConfig(watchCtx, configPaths, configDirectories, stdinEntry, optionsList)
	}
	instance, cancel, err := create(options)
	if err != nil {
		return err
	}
	runtimeDebug.FreeOSMemory()
	for {
		var fileChanged bool
		var entries []*OptionsEntry
		var readErr error
		select {
		case osSignal := <-osSignals:
			if osSignal != syscall.SIGHUP {
				err = closeService(instance, cancel)
				if err != nil {
					log.Error(E.Cause(err, "sing-box did not closed properly"))
				}
				return nil
			}
			entries, readErr = readConfigContents(configPaths, configDirectories, stdinEntry)
		case change := <-configChanges:
			fileChanged = true
			entries, readErr = change.entries, change.err
		}
		if readErr != nil {
			log.Error(E.Cause(readErr, "reload service"))
			continue
		}
		if fileChanged && sameConfigContents(entries, optionsList) {
			continue
		}
		entries, err = decodeConfigs(entries)
		if err != nil {
			log.Error(E.Cause(err, "reload service"))
			continue
		}
		instance, cancel, err = reloadService(instance, cancel, entries, optionsList, osSignals)
		if instance == nil {
			return err
		}
		if err != nil {
			log.Error(E.Cause(err, "reload service"))
			continue
		}
		optionsList = entries
		log.Info("configuration reloaded")
		runtimeDebug.FreeOSMemory()
	}
}

func reloadService(instance *box.Box, cancel context.CancelFunc, entries, previousEntries []*OptionsEntry, osSignals <-chan os.Signal) (*box.Box, context.CancelFunc, error) {
	options, err := mergeOptionsList(entries)
	if err != nil {
		return instance, cancel, err
	}
	candidate, candidateCancel, err := newService(options)
	if err != nil {
		return instance, cancel, err
	}
	if err = closeService(instance, cancel); err != nil {
		log.Error(E.Cause(err, "close service for reload"))
	}
	err = startService(candidate, candidateCancel)
	if err == nil {
		return candidate, candidateCancel, nil
	}
	select {
	case osSignal := <-osSignals:
		if osSignal != syscall.SIGHUP {
			return nil, nil, nil
		}
	default:
	}
	// Decode the saved contents again because creating an instance can mutate options.
	previousEntries, restoreErr := decodeConfigs(previousEntries)
	if restoreErr == nil {
		options, restoreErr = mergeOptionsList(previousEntries)
	}
	if restoreErr == nil {
		instance, cancel, restoreErr = create(options)
	}
	if restoreErr != nil {
		return nil, nil, E.Errors(err, E.Cause(restoreErr, "restore previous configuration"))
	}
	return instance, cancel, E.Cause(err, "previous configuration restored")
}

func closeService(instance *box.Box, cancel context.CancelFunc) error {
	cancel()
	closeCtx, closed := context.WithCancel(context.Background())
	defer closed()
	go closeMonitor(closeCtx)
	return instance.Close()
}

func closeMonitor(ctx context.Context) {
	time.Sleep(C.FatalStopTimeout)
	select {
	case <-ctx.Done():
		return
	default:
	}
	log.Fatal("sing-box did not close!")
}
