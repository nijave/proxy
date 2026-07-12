//go:build darwin && cgo

package main

import (
	"fmt"

	"github.com/energye/systray"
	"github.com/webview/webview"
)

func openGUI(guiURL string) error {
	fmt.Printf("\nDashboard: %s\n", guiURL)
	fmt.Println("Opening native window...")

	wv := webview.New(false)
	wv.SetTitle("routatic-proxy")
	wv.SetSize(1200, 800, webview.HintNone)
	wv.Navigate(guiURL)

	// Set up system tray
	systray.Run(func() {
		systray.SetTitle("routatic-proxy")
		systray.SetTooltip("routatic-proxy is running")
		mQuit := systray.AddMenuItem("Quit", "Stop the proxy")
		go func() {
			<-mQuit.ClickedCh
			wv.Dispatch(func() {
				wv.Terminate()
			})
		}()
	}, nil)

	wv.Run()
	wv.Destroy()
	return nil
}
