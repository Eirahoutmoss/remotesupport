//go:build windows

package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	clientsignaling "github.com/eirahoutmoss/remotesupport/client/signaling"
	webrtcpeer "github.com/eirahoutmoss/remotesupport/client/webrtc"
	"github.com/pion/webrtc/v4"
)

var (
	userStopped   atomic.Bool
	everConnected atomic.Bool
	reconnecting  atomic.Bool
)

func peerDead(p *webrtcpeer.Peer) bool {
	switch p.ConnectionState() {
	case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
		return true
	}
	return false
}

func onPeerDropped(role int) {
	if userStopped.Load() || !everConnected.Load() {
		return
	}
	netlogf("BAĞLANTI KOPTU süre=%s fps=%d", sessionElapsed(), fpsValue.Load())
	if role == 2 {
		setStatus("● Bağlantı koptu — otomatik yeniden bağlanılıyor…")
		go operatorReconnectLoop()
	} else {
		setStatus("● Bağlantı koptu — karşı tarafın yeniden bağlanması bekleniyor…")
	}
}

// operatorNegotiate builds a fresh peer and offer over the still-open signaling
// channel. Used both for the initial connect's retry and for auto-reconnect.
func operatorNegotiate(ctx context.Context, c *clientsignaling.Client) error {
	peer, err := newPeer(ctx, c, true)
	if err != nil {
		return err
	}
	peer.SetScreenHandler(setViewerFrame)
	peer.SetControlHandler(func(msg string) { defer logCrash("control"); handleRemoteInput(peer, msg) })
	peer.SetFileHandler(func(d []byte) { defer logCrash("file"); handleRemoteFile(d) })
	if err := peer.CreateControl(); err != nil {
		return err
	}
	if err := peer.CreateFile(); err != nil {
		return err
	}
	if err := peer.CreateScreen(); err != nil {
		return err
	}
	offer, err := peer.Offer(ctx)
	if err != nil {
		return err
	}
	state.mu.Lock()
	old := state.peer
	state.peer = peer
	state.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	if err := c.Signal(ctx, offer.Kind, offer.Payload); err != nil {
		return err
	}
	go watchPeer(peer)
	return nil
}

func operatorReconnectLoop() {
	if !reconnecting.CompareAndSwap(false, true) {
		return
	}
	defer reconnecting.Store(false)
	for attempt := 1; attempt <= 6; attempt++ {
		if userStopped.Load() {
			return
		}
		time.Sleep(time.Duration(attempt) * time.Second)
		if userStopped.Load() {
			return
		}
		state.mu.Lock()
		ctx := state.ctx
		c := state.sig
		state.mu.Unlock()
		if ctx == nil || c == nil {
			setStatus("● Yeniden bağlanılamadı — elle yeniden bağlanın.")
			return
		}
		setStatus(fmt.Sprintf("● Yeniden bağlanılıyor… (deneme %d/6)", attempt))
		netlogf("Yeniden bağlanma denemesi %d/6", attempt)
		if err := operatorNegotiate(ctx, c); err != nil {
			continue
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if userStopped.Load() {
				return
			}
			state.mu.Lock()
			p := state.peer
			state.mu.Unlock()
			if p != nil && p.ConnectionState() == webrtc.PeerConnectionStateConnected {
				return
			}
			time.Sleep(300 * time.Millisecond)
		}
	}
	netlogf("Otomatik yeniden bağlanma BAŞARISIZ")
	setStatus("● Otomatik yeniden bağlanma başarısız. 'Bağlantıyı Kes' ile kapatıp yeniden deneyin.")
}

// targetRebuild recreates the target-side peer when a renegotiation offer arrives
// after a drop (auto-reconnect), mirroring the initial peer_joined setup.
func targetRebuild(ctx context.Context, c *clientsignaling.Client) *webrtcpeer.Peer {
	peer, err := newPeer(ctx, c, true)
	if err != nil {
		setStatus("● Yeniden bağlantı kurulamadı: " + err.Error())
		return nil
	}
	peer.SetControlHandler(func(msg string) { defer logCrash("control"); handleRemoteInput(peer, msg) })
	peer.SetFileHandler(func(d []byte) { defer logCrash("file"); handleRemoteFile(d) })
	state.mu.Lock()
	old := state.peer
	state.peer = peer
	state.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	setStatus("● Karşı taraf yeniden bağlanıyor…")
	startCapture(peer, ctx)
	go watchPeer(peer)
	return peer
}
