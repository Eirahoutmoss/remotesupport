//go:build windows

package main

import (
	"context"
	"os"
	"strings"
	"sync/atomic"
	"time"

	clientsignaling "github.com/eirahoutmoss/remotesupport/client/signaling"
)

// deviceModeActive is set while this machine is serving technician access.
var deviceModeActive atomic.Bool

// deviceModeGen tokens the running loop. startDeviceMode launches a loop with a
// token; stopDeviceMode (and any restart) advances it so the old loop exits.
// This guarantees exactly one announce loop — multiple concurrent loops were
// registering in a tight cycle, overwriting each other on the server and
// tripping its create rate-limiter, which left the device unreachable.
var deviceModeGen atomic.Int64

// effectiveDeviceID is the configured id, or the hostname as a sensible default.
func effectiveDeviceID() string {
	if id := strings.TrimSpace(currentSettings().DeviceID); id != "" {
		return id
	}
	h, _ := os.Hostname()
	return strings.TrimSpace(h)
}

// startDeviceMode registers this machine with the server (device id + password)
// and waits for technician connections, re-registering after each session. It
// is idempotent: if a loop is already running it does nothing, so the app
// launch, REGDEV, and the settings page can all call it safely.
func startDeviceMode() {
	cfg := currentSettings()
	if !cfg.DeviceAccessOn {
		return
	}
	// Escape hatch for single-machine testing: a second instance launched with
	// REMOTESUPPORT_NO_DEVICE set will act purely as technician and not announce
	// the shared device id, so the two instances don't evict each other.
	if strings.TrimSpace(os.Getenv("REMOTESUPPORT_NO_DEVICE")) != "" {
		netlogf("Cihaz erişimi REMOTESUPPORT_NO_DEVICE ile bu örnekte kapalı")
		return
	}
	id := effectiveDeviceID()
	pass := cfg.DevicePass
	if id == "" || len(pass) < 4 {
		netlogf("Cihaz erişimi açık ama kimlik/parola eksik — devre dışı")
		return
	}
	// One loop only. CAS fails if a loop is already active.
	if !deviceModeActive.CompareAndSwap(false, true) {
		return
	}
	ensureFirewall()
	startEmbeddedSignaling()
	gen := deviceModeGen.Add(1)
	setActiveCode(id)
	// Run in the background: the app stays on whatever screen the user is on
	// (Home on launch). A connecting technician triggers the approval dialog
	// and then the live session view; status reflects it meanwhile.
	setStatus("● Cihaz erişimi arka planda etkin — teknisyen kimlik ve parola ile bağlanabilir.")
	go deviceModeLoop(id, pass, gen)
}

func deviceModeLoop(id, pass string, gen int64) {
	defer logCrash("deviceMode")
	// Release the active flag only if this loop is still the current one; a
	// stop or restart that advanced the generation owns the flag instead.
	defer func() {
		if deviceModeGen.Load() == gen {
			deviceModeActive.Store(false)
		}
	}()
	const baseBackoff = 3 * time.Second
	const maxBackoff = 30 * time.Second
	backoff := baseBackoff
	for {
		if deviceModeGen.Load() != gen {
			return // superseded by stop or a newer start
		}
		ep := signalingEndpoint()
		ctx, cancel := context.WithCancel(context.Background())
		c, err := clientsignaling.Dial(ctx, ep)
		if err != nil {
			netlogf("Cihaz erişimi: sunucuya bağlanılamadı (%s): %v", ep, err)
			cancel()
			sleepGen(backoff, gen)
			backoff = nextBackoff(backoff, maxBackoff)
			continue
		}
		c.App = appVersion
		if err := c.CreateDevice(ctx, id, pass); err != nil {
			_ = c.Close(context.Background())
			cancel()
			netlogf("Cihaz kaydı başarısız: %v", err)
			// rate_limited means we (or a prior build's duplicate loop) hit the
			// server too fast; wait well clear of its window before retrying.
			wait := 15 * time.Second
			if strings.Contains(err.Error(), "rate_limited") {
				wait = 20 * time.Second
			}
			sleepGen(wait, gen)
			continue
		}
		netlogf("Cihaz erişimi çevrimiçi: %s (sunucu=%s)", id, ep)
		state.mu.Lock()
		state.ctx, state.cancel, state.sig = ctx, cancel, c
		state.mu.Unlock()
		if !capturing.Load() {
			setStatus("● Cihaz erişimi etkin — bağlantı bekleniyor (kimlik: " + id + ").")
		}
		// Serve one technician session (blocks until the signaling link drops),
		// then re-register so the device stays reachable.
		start := time.Now()
		targetLoop(ctx, c)
		_ = c.Close(context.Background())
		cancel()
		if deviceModeGen.Load() != gen {
			return
		}
		// A link that dropped almost at once was most likely evicted by another
		// registration of the same device id (a duplicate instance, or a restart
		// race). Grow the backoff so two instances don't ping-pong every few
		// seconds; a link that lasted was a real session, so reset to base.
		if time.Since(start) < 5*time.Second {
			netlogf("Cihaz oturumu hemen düştü — %s sonra tekrar denenecek (aynı kimlikle başka bir örnek olabilir)", backoff)
			sleepGen(backoff, gen)
			backoff = nextBackoff(backoff, maxBackoff)
		} else {
			backoff = baseBackoff
			sleepGen(backoff, gen)
		}
	}
}

func nextBackoff(cur, max time.Duration) time.Duration {
	if n := cur * 2; n < max {
		return n
	}
	return max
}

// sleepGen waits up to d, waking early if this loop was superseded.
func sleepGen(d time.Duration, gen int64) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if deviceModeGen.Load() != gen {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// stopDeviceMode ends background device access immediately (Settings → Kapalı).
func stopDeviceMode() {
	if !deviceModeActive.Load() {
		return
	}
	deviceModeGen.Add(1) // supersede the running loop
	deviceModeActive.Store(false)
	state.mu.Lock()
	c, cancel := state.sig, state.cancel
	state.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if c != nil {
		_ = c.Close(context.Background())
	}
	netlogf("Cihaz erişimi kapatıldı")
}
