package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type logView struct {
	mu       sync.Mutex
	entry    *widget.Entry
	scroll   *container.Scroll
	queue    []string
	lines    []string
	maxLines int
	flushCh  chan struct{}
}

func newLogView() *logView {
	e := widget.NewMultiLineEntry()
	e.Wrapping = fyne.TextWrapBreak
	e.TextStyle = fyne.TextStyle{Monospace: true}
	e.SetPlaceHolder("Output will appear here...")
	s := container.NewVScroll(e)

	l := &logView{
		entry:    e,
		scroll:   s,
		queue:    make([]string, 0, 128),
		lines:    make([]string, 0, 512),
		maxLines: 2000,
		flushCh:  make(chan struct{}, 1),
	}

	go l.batchFlusher()
	return l
}

func (l *logView) batchFlusher() {
	ticker := time.NewTicker(60 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.flush()
		case <-l.flushCh:
			l.flush()
		}
	}
}

func (l *logView) flush() {
	l.mu.Lock()
	if len(l.queue) == 0 {
		l.mu.Unlock()
		return
	}

	toAdd := l.queue
	l.queue = make([]string, 0, 128)

	l.lines = append(l.lines, toAdd...)
	if len(l.lines) > l.maxLines {
		l.lines = l.lines[len(l.lines)-l.maxLines:]
	}
	text := strings.Join(l.lines, "\n")
	l.mu.Unlock()

	l.entry.SetText(text)
	l.scroll.ScrollToBottom()
}

func (l *logView) Append(line string) {
	l.mu.Lock()
	l.queue = append(l.queue, line)
	l.mu.Unlock()
}

func (l *logView) FlushNow() {
	select {
	case l.flushCh <- struct{}{}:
	default:
	}
}

func (l *logView) Clear() {
	l.mu.Lock()
	l.queue = l.queue[:0]
	l.lines = l.lines[:0]
	l.mu.Unlock()
	l.entry.SetText("")
}

func (l *logView) CanvasObject() fyne.CanvasObject {
	return l.scroll
}

type state struct {
	mu      sync.Mutex
	binPath string
	ipaPath string
}

func (s *state) setBinPath(p string) {
	s.mu.Lock()
	s.binPath = p
	s.mu.Unlock()
}

func (s *state) setIPAPath(p string) {
	s.mu.Lock()
	s.ipaPath = p
	s.mu.Unlock()
}

func (s *state) get() (bin, ipa string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.binPath, s.ipaPath
}

type appProfile struct {
	AppleID      string `json:"apple_id"`
	Password     string `json:"password,omitempty"`
	AltServerBin string `json:"altserver_bin,omitempty"`
}

func loadProfile(path string) (*appProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p appProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func saveProfile(path string, p *appProfile) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func main() {
	a := app.NewWithID("com.artcevvv.althelper")
	w := a.NewWindow("AltHelper — iOS Sideload Assistant")

	st := &state{}
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".local", "share", "althelper")
	_ = os.MkdirAll(dataDir, 0o755)
	profilePath := filepath.Join(dataDir, "profile.json")
	savedProfile, _ := loadProfile(profilePath)

	lv := newLogView()

	// 1. Anisette Server
	anisetteContainerStatus := widget.NewLabel("Container: checking...")
	anisetteContainerStatus.Wrapping = fyne.TextWrapBreak
	anisetteHealthStatus := widget.NewLabel("Health: checking...")
	anisetteHealthStatus.Wrapping = fyne.TextWrapBreak
	anisetteBusy := widget.NewProgressBarInfinite()
	anisetteBusy.Hide()

	refreshAnisetteStatus := func() {
		go func() {
			cStatus := getAnisetteContainerStatus()
			healthy, detail := checkAnisetteHealth()
			hasPort := isAnisettePortMapped()

			var containerText string
			if cStatus == "running" && !hasPort {
				containerText = fmt.Sprintf("Container: running WITHOUT port 6969! (%s)", anisetteContainerName)
			} else {
				containerText = fmt.Sprintf("Container: %s (%s)", cStatus, anisetteContainerName)
			}

			var healthText string
			if healthy {
				healthText = "Health: " + detail
			} else {
				if cStatus == "running" {
					healthText = "Health: Unhealthy (" + detail + ")"
				} else {
					healthText = "Health: Server not running"
				}
			}

			anisetteContainerStatus.SetText(containerText)
			anisetteHealthStatus.SetText(healthText)
		}()
	}
	refreshAnisetteStatus()

	startAnisetteBtn := widget.NewButtonWithIcon("Start", theme.MediaPlayIcon(), nil)
	stopAnisetteBtn := widget.NewButtonWithIcon("Stop", theme.MediaStopIcon(), nil)
	checkHealthBtn := widget.NewButtonWithIcon("Check Health", theme.ViewRefreshIcon(), nil)
	anisetteBtns := container.NewGridWithColumns(3, startAnisetteBtn, stopAnisetteBtn, checkHealthBtn)

	startAnisetteBtn.OnTapped = func() {
		go func() {
			anisetteBusy.Show()
			anisetteBusy.Start()
			startAnisetteBtn.Disable()
			defer func() {
				anisetteBusy.Stop()
				anisetteBusy.Hide()
				startAnisetteBtn.Enable()
				refreshAnisetteStatus()
			}()

			lv.Append("---- Starting Anisette Server ----")
			if err := startAnisette(lv.Append); err != nil {
				lv.Append("ERROR starting anisette: " + err.Error())
				dialog.ShowError(err, w)
				return
			}
			lv.Append("Anisette server is active and verified healthy on :6969")
		}()
	}

	stopAnisetteBtn.OnTapped = func() {
		go func() {
			anisetteBusy.Show()
			anisetteBusy.Start()
			defer func() {
				anisetteBusy.Stop()
				anisetteBusy.Hide()
				refreshAnisetteStatus()
			}()
			lv.Append("---- Stopping Anisette Server ----")
			if err := stopAnisette(lv.Append); err != nil {
				lv.Append("ERROR stopping anisette: " + err.Error())
			} else {
				lv.Append("Anisette server stopped.")
			}
		}()
	}

	checkHealthBtn.OnTapped = func() {
		go func() {
			lv.Append("Running healthcheck on " + anisetteURL + " ...")
			cStatus := getAnisetteContainerStatus()
			healthy, detail := checkAnisetteHealth()
			hasPort := isAnisettePortMapped()

			if healthy && hasPort {
				lv.Append("Anisette Healthcheck: OK - " + detail)
			} else if !hasPort && cStatus == "running" {
				lv.Append("Anisette Healthcheck: FAILED - container is running but port 6969 is not published!")
			} else {
				lv.Append(fmt.Sprintf("Anisette Healthcheck: FAILED (%s, container status: %s)", detail, cStatus))
			}
			refreshAnisetteStatus()
		}()
	}

	anisetteCard := widget.NewCard("1. Anisette Server", "Authentication header service (:6969)",
		container.NewVBox(
			anisetteContainerStatus,
			anisetteHealthStatus,
			anisetteBtns,
			anisetteBusy,
		),
	)

	// 2. AltServer-Linux binary
	repoEntry := widget.NewEntry()
	repoEntry.SetText(defaultRepo)
	repoEntry.SetPlaceHolder("GitHub owner/repo")

	binPathLabel := widget.NewLabel("AltServer binary: not set")
	binPathLabel.Wrapping = fyne.TextWrapBreak
	setBinLabel := func(p string) {
		if p == "" {
			binPathLabel.SetText("AltServer binary: not set")
		} else {
			binPathLabel.SetText("AltServer binary: " + p)
		}
	}

	downloadBinBusy := widget.NewProgressBarInfinite()
	downloadBinBusy.Hide()

	downloadBtn := widget.NewButtonWithIcon("Download Binary", theme.DownloadIcon(), nil)
	browseBinBtn := widget.NewButtonWithIcon("Browse Binary...", theme.FolderOpenIcon(), nil)
	binBtns := container.NewGridWithColumns(2, downloadBtn, browseBinBtn)

	downloadBtn.OnTapped = func() {
		go func() {
			downloadBinBusy.Show()
			downloadBinBusy.Start()
			downloadBtn.Disable()
			defer func() {
				downloadBinBusy.Stop()
				downloadBinBusy.Hide()
				downloadBtn.Enable()
			}()

			hint, err := archAssetHint()
			if err != nil {
				lv.Append("ERROR: " + err.Error())
				dialog.ShowError(err, w)
				return
			}
			dest := filepath.Join(dataDir, "AltServer-"+hint)

			repo := strings.TrimSpace(repoEntry.Text)
			if repo == "" {
				repo = defaultRepo
			}

			lv.Append("---- Downloading AltServer ----")
			if err := downloadAltServer(repo, dest, lv.Append); err != nil {
				lv.Append("ERROR downloading AltServer: " + err.Error())
				dialog.ShowError(err, w)
				return
			}
			st.setBinPath(dest)
			setBinLabel(dest)

			prof, _ := loadProfile(profilePath)
			if prof == nil {
				prof = &appProfile{}
			}
			prof.AltServerBin = dest
			_ = saveProfile(profilePath, prof)

			lv.Append("AltServer ready at: " + dest)
		}()
	}

	browseBinBtn.OnTapped = func() {
		fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if reader == nil {
				return
			}
			defer reader.Close()
			path := reader.URI().Path()
			_ = os.Chmod(path, 0o755)
			st.setBinPath(path)
			setBinLabel(path)

			prof, _ := loadProfile(profilePath)
			if prof == nil {
				prof = &appProfile{}
			}
			prof.AltServerBin = path
			_ = saveProfile(profilePath, prof)

			lv.Append("Selected binary: " + path)
		}, w)
		fd.Show()
	}

	if savedProfile != nil && savedProfile.AltServerBin != "" {
		if info, err := os.Stat(savedProfile.AltServerBin); err == nil && !info.IsDir() {
			st.setBinPath(savedProfile.AltServerBin)
			setBinLabel(savedProfile.AltServerBin)
			lv.Append("Loaded AltServer binary from config: " + savedProfile.AltServerBin)
		}
	}

	if bin, _ := st.get(); bin == "" {
		if hint, err := archAssetHint(); err == nil {
			candidate := filepath.Join(dataDir, "AltServer-"+hint)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				st.setBinPath(candidate)
				setBinLabel(candidate)
				lv.Append("Found existing AltServer binary: " + candidate)
			}
		}
	}

	binCard := widget.NewCard("2. AltServer Binary", "Patched AltServer-Linux binary for iOS sideloading",
		container.NewVBox(
			widget.NewLabel("GitHub repository fork:"),
			repoEntry,
			binBtns,
			binPathLabel,
			downloadBinBusy,
		),
	)

	// 3. Target Device
	udidSelect := widget.NewSelect([]string{}, nil)
	udidSelect.PlaceHolder = "Select connected device..."

	refreshDevicesBtn := widget.NewButtonWithIcon("Refresh Devices", theme.ViewRefreshIcon(), nil)
	refreshDevicesAction := func() {
		go func() {
			refreshDevicesBtn.Disable()
			defer refreshDevicesBtn.Enable()
			lv.Append("Scanning for iOS devices over USB...")
			udids, err := listUDIDs()
			if err != nil {
				lv.Append("ERROR listing devices: " + err.Error())
				return
			}
			udidSelect.Options = udids
			udidSelect.Refresh()
			if len(udids) > 0 {
				udidSelect.SetSelected(udids[0])
			}
			lv.Append(fmt.Sprintf("Found %d device(s) over USB", len(udids)))
		}()
	}
	refreshDevicesBtn.OnTapped = refreshDevicesAction
	refreshDevicesAction()

	wifiCheck := widget.NewCheck("Install over Wi-Fi (requires netmuxd)", nil)
	netmuxdEntry := widget.NewEntry()
	netmuxdEntry.SetText("127.0.0.1:27015")
	netmuxdEntry.Disable()
	wifiCheck.OnChanged = func(checked bool) {
		if checked {
			netmuxdEntry.Enable()
		} else {
			netmuxdEntry.Disable()
		}
	}

	deviceCard := widget.NewCard("3. Target Device", "Select your connected iPhone or iPad",
		container.NewVBox(
			container.NewBorder(nil, nil, nil, refreshDevicesBtn, udidSelect),
			wifiCheck,
			netmuxdEntry,
		),
	)

	// 4. Apple ID & IPA
	appleIDEntry := widget.NewEntry()
	appleIDEntry.SetPlaceHolder("name@icloud.com")

	passwordEntry := widget.NewPasswordEntry()
	passwordEntry.SetPlaceHolder("App-specific password (or account password)")

	rememberProfileCheck := widget.NewCheck("Remember Apple ID profile in local config", nil)

	if savedProfile != nil {
		if savedProfile.AppleID != "" {
			appleIDEntry.SetText(savedProfile.AppleID)
			rememberProfileCheck.SetChecked(true)
			lv.Append("Loaded saved Apple ID profile: " + savedProfile.AppleID)
		}
		if savedProfile.Password != "" {
			passwordEntry.SetText(savedProfile.Password)
		}
	}

	ipaLabel := widget.NewLabel("No .ipa file selected")
	ipaLabel.Wrapping = fyne.TextWrapBreak
	pickIPABtn := widget.NewButtonWithIcon("Choose .ipa File...", theme.FolderOpenIcon(), nil)
	pickIPABtn.OnTapped = func() {
		fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if reader == nil {
				return
			}
			defer reader.Close()
			path := reader.URI().Path()
			st.setIPAPath(path)
			ipaLabel.SetText(filepath.Base(path) + " (" + path + ")")
			lv.Append("Selected IPA: " + path)
		}, w)
		fd.SetFilter(storage.NewExtensionFileFilter([]string{".ipa"}))
		fd.Show()
	}

	credForm := widget.NewForm(
		widget.NewFormItem("Apple ID", appleIDEntry),
		widget.NewFormItem("Password", passwordEntry),
	)

	credCard := widget.NewCard("4. Apple ID & App", "Credentials for signing and IPA file to sideload",
		container.NewVBox(
			credForm,
			rememberProfileCheck,
			pickIPABtn,
			ipaLabel,
		),
	)

	prompt2FA := func() (string, error) {
		codeChan := make(chan string, 1)

		codeEntry := widget.NewEntry()
		codeEntry.SetPlaceHolder("123456")
		codeEntry.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}

		var dlg dialog.Dialog
		var closeOnce sync.Once
		submitAction := func(confirmed bool) {
			closeOnce.Do(func() {
				if confirmed {
					codeChan <- strings.TrimSpace(codeEntry.Text)
				} else {
					codeChan <- ""
				}
				if dlg != nil {
					dlg.Hide()
				}
			})
		}

		codeEntry.OnSubmitted = func(_ string) {
			submitAction(true)
		}

		dlg = dialog.NewCustomConfirm(
			"Two-Factor Authentication",
			"Submit Code",
			"Cancel",
			container.NewVBox(
				widget.NewLabelWithStyle("Apple ID Verification Required", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				widget.NewLabel("Enter the 6-digit code displayed on your Apple devices:"),
				codeEntry,
			),
			submitAction,
			w,
		)
		dlg.Resize(fyne.NewSize(420, 220))
		dlg.Show()
		w.Canvas().Focus(codeEntry)

		code := <-codeChan
		if code == "" {
			return "", fmt.Errorf("2FA input was cancelled")
		}
		return code, nil
	}

	// 5. Installation
	installBusy := widget.NewProgressBarInfinite()
	installBusy.Hide()

	installBtn := widget.NewButtonWithIcon("Install to Device", theme.ConfirmIcon(), nil)
	installBtn.Importance = widget.HighImportance
	installBtn.OnTapped = func() {
		bin, ipa := st.get()
		udid := udidSelect.Selected
		appleID := strings.TrimSpace(appleIDEntry.Text)
		password := passwordEntry.Text
		wifi := wifiCheck.Checked
		netmuxdAddr := netmuxdEntry.Text

		if bin == "" {
			dialog.ShowInformation("Missing Binary", "Download or select an AltServer binary first (step 2).", w)
			return
		}
		if udid == "" {
			dialog.ShowInformation("Missing Device", "Select a device (click Refresh Devices in step 3).", w)
			return
		}
		if appleID == "" || password == "" {
			dialog.ShowInformation("Missing Credentials", "Enter your Apple ID and password (step 4).", w)
			return
		}
		if ipa == "" {
			dialog.ShowInformation("Missing IPA", "Choose an .ipa file to install (step 4).", w)
			return
		}

		executeInstall := func() {
			prof, _ := loadProfile(profilePath)
			if prof == nil {
				prof = &appProfile{}
			}
			prof.AltServerBin = bin
			if rememberProfileCheck.Checked {
				prof.AppleID = appleID
				prof.Password = password
			}
			_ = saveProfile(profilePath, prof)

			go func() {
				installBusy.Show()
				installBusy.Start()
				installBtn.Disable()
				defer func() {
					installBusy.Stop()
					installBusy.Hide()
					installBtn.Enable()
				}()

				lv.Append("========================================")
				lv.Append("Starting app installation...")
				lv.Append("========================================")
				err := installApp(installParams{
					BinPath:     bin,
					UDID:        udid,
					AppleID:     appleID,
					Password:    password,
					IPAPath:     ipa,
					WifiMode:    wifi,
					NetmuxdAddr: netmuxdAddr,
				}, lv.Append, prompt2FA)

				if err != nil {
					lv.Append("INSTALL FAILED: " + err.Error())
					dialog.ShowError(err, w)
					return
				}
				lv.Append("========================================")
				lv.Append("Install complete! Check your iOS device.")
				lv.Append("========================================")
				dialog.ShowInformation("Installation Complete", "AltServer finished successfully.\nCheck your device's home screen.", w)
			}()
		}

		// Prompt to save profile if not yet saved and checkbox is unchecked
		if _, err := os.Stat(profilePath); os.IsNotExist(err) && !rememberProfileCheck.Checked {
			dialog.ShowConfirm(
				"Save Apple ID Profile?",
				"Would you like to save this Apple ID profile to local config for future sessions?",
				func(save bool) {
					prof, _ := loadProfile(profilePath)
					if prof == nil {
						prof = &appProfile{}
					}
					prof.AltServerBin = bin
					if save {
						rememberProfileCheck.SetChecked(true)
						prof.AppleID = appleID
						prof.Password = password
						lv.Append("Saved Apple ID profile to " + profilePath)
					}
					_ = saveProfile(profilePath, prof)
					executeInstall()
				},
				w,
			)
			return
		}

		executeInstall()
	}

	installCard := widget.NewCard("5. Installation", "Sign and transfer the app to your device",
		container.NewVBox(
			installBtn,
			installBusy,
		),
	)

	// Layout: Left controls, Right live log
	controls := container.NewVBox(
		anisetteCard,
		binCard,
		deviceCard,
		credCard,
		installCard,
	)
	controlsScroll := container.NewVScroll(container.NewPadded(controls))

	logHeader := container.NewBorder(
		nil, nil,
		widget.NewLabelWithStyle("Live Activity Log", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewButtonWithIcon("Clear Log", theme.DeleteIcon(), func() {
			lv.Clear()
		}),
	)
	logPanel := container.NewBorder(logHeader, nil, nil, nil, lv.CanvasObject())

	split := container.NewHSplit(controlsScroll, logPanel)
	split.SetOffset(0.5)

	w.SetContent(split)
	w.Resize(fyne.NewSize(1040, 720))
	w.ShowAndRun()
}
