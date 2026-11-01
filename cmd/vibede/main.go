// Command vibede opens VibeDE in a native GTK/WebKit window. The HTML/Go-WASM
// interface is embedded in that window; no external browser or public port is
// used in native mode.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"vibede/internal/apphost"
	"vibede/internal/config"
	"vibede/internal/desktop"
	"vibede/internal/version"
)

func main() {
	root := flag.String("root", "", "workspace root; native mode shows a folder chooser when omitted")
	host := flag.String("host", "127.0.0.1", "listen host for browser/headless mode")
	port := flag.Int("port", 0, "listen port; 0 lets the kernel allocate one")
	ui := flag.String("ui", "native", "native (GTK/WebKit window), browser, or headless")
	openBrowserFlag := flag.Bool("open", false, "open in the external browser (same as --ui browser)")
	noServers := flag.Bool("no-servers", false, "disable all language servers")
	aiURL := flag.String("ai-url", "", "OpenAI-compatible base URL for selection explanations (default http://127.0.0.1:8080/v1)")
	aiModel := flag.String("ai-model", "", "model id for explanations; empty uses the first model the endpoint serves")
	aiKey := flag.String("ai-key", "", "API key for the explanation endpoint (also VIBEDE_AI_KEY)")
	noAI := flag.Bool("no-ai", false, "disable AI selection explanations")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("VibeDE %s\n", version.Version)
		return
	}

	mode := *ui
	if *openBrowserFlag {
		mode = "browser"
	}
	switch mode {
	case "native", "browser", "headless":
	default:
		log.Fatalf("vibede: --ui must be native, browser or headless (got %q)", mode)
	}

	ai := aiOverride(*aiURL, *aiModel, *aiKey, *noAI)
	if mode == "native" {
		runNative(*root, *host, *port, *noServers, ai)
		return
	}
	runHeadless(*root, *host, *port, *noServers, ai, mode == "browser")
}

// aiOverride builds the flag override, or nil when no AI flag was set.
func aiOverride(url, model, key string, off bool) *config.AIConfig {
	if !off && url == "" && model == "" && key == "" {
		return nil
	}
	return &config.AIConfig{Endpoint: url, Model: model, APIKey: key, Disabled: off}
}

func runNative(root, host string, port int, noServers bool, ai *config.AIConfig) {
	if !desktop.Available() {
		log.Fatalf("vibede: native UI unavailable: need Linux with GTK3 + WebKitGTK 4.1 and a display\n" +
			"        use --ui browser (external browser) or --ui headless (only print the URL)")
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		log.Printf("vibede: native mode keeps the embedded server on loopback; ignoring --host %s", host)
	}
	if port != 0 {
		log.Printf("vibede: native mode uses an ephemeral internal port; ignoring --port %d", port)
	}
	manager := apphost.New(apphost.Options{
		ListenHost:   "127.0.0.1",
		URLHost:      "127.0.0.1",
		Port:         0,
		NoServers:    noServers,
		AI:           ai,
		WindowOpener: desktop.OpenWindow,
	})
	start := func(nextRoot string) (string, error) {
		url, err := manager.Start(nextRoot)
		if err != nil {
			return "", err
		}
		log.Printf("workspace %s · internal %s", manager.Root(), url)
		return url, nil
	}
	switcher := func(nextRoot string) (string, error) {
		url, err := manager.Switch(nextRoot)
		if err != nil {
			return "", err
		}
		log.Printf("workspace switched to %s · internal %s", manager.Root(), url)
		return url, nil
	}
	err := desktop.Run(desktop.Options{
		Title:       "VibeDE",
		Width:       1600,
		Height:      1000,
		InitialRoot: root,
		Start:       start,
		Switch:      switcher,
	})
	manager.Close()
	if errors.Is(err, desktop.ErrCanceled) {
		return
	}
	if err != nil {
		log.Fatalf("vibede: %v", err)
	}
}

func runHeadless(root, host string, port int, noServers bool, ai *config.AIConfig, openBrowser bool) {
	if root == "" {
		root = "."
	}
	if host == "" {
		host = "127.0.0.1"
	}
	manager := apphost.New(apphost.Options{
		ListenHost: host,
		URLHost:    host,
		Port:       port,
		NoServers:  noServers,
		AI:         ai,
	})
	url, err := manager.Start(root)
	if err != nil {
		log.Fatalf("vibede: %v", err)
	}
	defer manager.Close()

	fmt.Printf("VibeDE %s\n", version.Version)
	fmt.Printf("  workspace : %s\n", manager.Root())
	fmt.Printf("  url       : %s\n", url)
	modeLabel := "headless"
	if openBrowser {
		modeLabel = "external browser"
	}
	fmt.Printf("  mode      : read-only · %s\n", modeLabel)
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		fmt.Printf("  warning   : listening on %s; VibeDE has no authentication\n", host)
	}
	if openBrowser {
		openExternalBrowser(url)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}

func openExternalBrowser(url string) {
	cmd := exec.Command("xdg-open", url)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		log.Printf("vibede: could not open browser: %v", err)
		return
	}
	go func() { _ = cmd.Wait() }()
}
