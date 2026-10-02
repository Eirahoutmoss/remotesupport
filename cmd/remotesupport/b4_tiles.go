//go:build windows

package main

// Tile/delta screen encoding. Instead of a full JPEG every frame, the agent
// compares the new capture with the last one it sent in 64x64 tiles and ships
// only the changed tiles, packed into one "atlas" JPEG. The tile positions
// travel in a JPEG COM segment, so the existing frame format, transport and
// validation are unchanged. Unchanged screens send nothing at all.

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
	"image/jpeg"
	"sync"
	"sync/atomic"
	"time"
)

const (
	tileSize     = 64
	atlasCols    = 16
	keyInterval  = 5 * time.Second
	deltaMaxPart = 0.6 // above this share of changed tiles a keyframe is cheaper
	tileMagic    = "NXT1"
)

type tileEncoder struct {
	prev     *image.RGBA
	monitor  int
	lastKey  time.Time
	forceKey atomic.Bool
}

// activeTiles is the encoder of the running capture loop.
var activeTiles atomic.Pointer[tileEncoder]

// requestKeyframe makes the next frame a full one (new viewer, lost base).
func requestKeyframe() {
	if e := activeTiles.Load(); e != nil {
		e.forceKey.Store(true)
	}
}

func (e *tileEncoder) reset() { e.prev = nil }

// encode returns the payload to send, or skip=true when nothing changed.
func (e *tileEncoder) encode(img *image.RGBA, monitor, quality int) (payload []byte, skip bool, err error) {
	b := img.Bounds()
	key := e.prev == nil || e.prev.Bounds() != b || e.monitor != monitor ||
		time.Since(e.lastKey) > keyInterval || e.forceKey.Swap(false)
	var dirty []image.Point
	if !key {
		dirty = changedTiles(e.prev, img)
		if len(dirty) == 0 {
			return nil, true, nil
		}
		total := ((b.Dx() + tileSize - 1) / tileSize) * ((b.Dy() + tileSize - 1) / tileSize)
		if float64(len(dirty)) > deltaMaxPart*float64(total) {
			key = true
		}
	}
	var buf bytes.Buffer
	if key {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, false, err
		}
		e.lastKey = time.Now()
		payload = buf.Bytes()
	} else {
		cols := min(len(dirty), atlasCols)
		rows := (len(dirty) + cols - 1) / cols
		atlas := image.NewRGBA(image.Rect(0, 0, cols*tileSize, rows*tileSize))
		for i, t := range dirty {
			src := image.Rect(t.X*tileSize, t.Y*tileSize, (t.X+1)*tileSize, (t.Y+1)*tileSize).Intersect(b)
			dp := image.Pt((i%cols)*tileSize, (i/cols)*tileSize)
			draw.Draw(atlas, image.Rectangle{dp, dp.Add(src.Size())}, img, src.Min, draw.Src)
		}
		if err := jpeg.Encode(&buf, atlas, &jpeg.Options{Quality: quality}); err != nil {
			return nil, false, err
		}
		payload = withTileMeta(buf.Bytes(), cols, dirty)
	}
	e.prev, e.monitor = img, monitor
	return payload, false, nil
}

// changedTiles compares two equally sized frames tile by tile, row by row.
func changedTiles(a, b *image.RGBA) []image.Point {
	w, h := b.Bounds().Dx(), b.Bounds().Dy()
	tw, th := (w+tileSize-1)/tileSize, (h+tileSize-1)/tileSize
	var out []image.Point
	for ty := 0; ty < th; ty++ {
		y0, y1 := ty*tileSize, min((ty+1)*tileSize, h)
		for tx := 0; tx < tw; tx++ {
			x0, x1 := tx*tileSize*4, min((tx+1)*tileSize, w)*4
			for y := y0; y < y1; y++ {
				ra := a.Pix[y*a.Stride+x0 : y*a.Stride+x1]
				rb := b.Pix[y*b.Stride+x0 : y*b.Stride+x1]
				if !bytes.Equal(ra, rb) {
					out = append(out, image.Pt(tx, ty))
					break
				}
			}
		}
	}
	return out
}

// withTileMeta inserts a COM segment right after SOI:
// "NXT1" | u16 tileSize | u16 cols | u16 count | count × (u16 tx, u16 ty)
func withTileMeta(jp []byte, cols int, tiles []image.Point) []byte {
	meta := make([]byte, 0, 10+4*len(tiles))
	meta = append(meta, tileMagic...)
	meta = binary.BigEndian.AppendUint16(meta, tileSize)
	meta = binary.BigEndian.AppendUint16(meta, uint16(cols))
	meta = binary.BigEndian.AppendUint16(meta, uint16(len(tiles)))
	for _, t := range tiles {
		meta = binary.BigEndian.AppendUint16(meta, uint16(t.X))
		meta = binary.BigEndian.AppendUint16(meta, uint16(t.Y))
	}
	out := make([]byte, 0, len(jp)+len(meta)+4)
	out = append(out, jp[:2]...) // SOI
	out = append(out, 0xFF, 0xFE)
	out = binary.BigEndian.AppendUint16(out, uint16(len(meta)+2))
	out = append(out, meta...)
	return append(out, jp[2:]...)
}

type tileMeta struct {
	size, cols int
	tiles      []image.Point
}

func parseTileMeta(jp []byte) (tileMeta, bool) {
	if len(jp) < 14 || jp[2] != 0xFF || jp[3] != 0xFE {
		return tileMeta{}, false
	}
	l := int(binary.BigEndian.Uint16(jp[4:6]))
	if l < 2+10 || 4+l > len(jp) || string(jp[6:10]) != tileMagic {
		return tileMeta{}, false
	}
	m := jp[6 : 4+l]
	tm := tileMeta{size: int(binary.BigEndian.Uint16(m[4:6])), cols: int(binary.BigEndian.Uint16(m[6:8]))}
	n := int(binary.BigEndian.Uint16(m[8:10]))
	if tm.size <= 0 || tm.cols <= 0 || len(m) != 10+4*n {
		return tileMeta{}, false
	}
	for i := 0; i < n; i++ {
		o := 10 + 4*i
		tm.tiles = append(tm.tiles, image.Pt(int(binary.BigEndian.Uint16(m[o:])), int(binary.BigEndian.Uint16(m[o+2:]))))
	}
	return tm, true
}

var keyReqMu sync.Mutex
var keyReqAt time.Time

// applyTileDelta patches the viewer's BGRA buffer with the tiles of a delta
// frame. Returns false (and asks for a keyframe) when there is no matching base.
func applyTileDelta(width, height int, jp []byte, tm tileMeta) bool {
	img, err := jpeg.Decode(bytes.NewReader(jp))
	if err != nil {
		return false
	}
	ab := img.Bounds()
	atlas := image.NewRGBA(image.Rect(0, 0, ab.Dx(), ab.Dy()))
	draw.Draw(atlas, atlas.Bounds(), img, ab.Min, draw.Src)
	viewer.mu.Lock()
	ok := viewer.pixels != nil && viewer.width == width && viewer.height == height
	if ok {
		px := viewer.pixels
		for i, t := range tm.tiles {
			ax, ay := (i%tm.cols)*tm.size, (i/tm.cols)*tm.size
			x0, y0 := t.X*tm.size, t.Y*tm.size
			w, h := min(tm.size, width-x0), min(tm.size, height-y0)
			if w <= 0 || h <= 0 || ax+w > atlas.Rect.Dx() || ay+h > atlas.Rect.Dy() {
				continue
			}
			for y := 0; y < h; y++ {
				s := atlas.Pix[(ay+y)*atlas.Stride+ax*4:]
				d := px[((y0+y)*width+x0)*4:]
				for x := 0; x < w; x++ {
					d[x*4], d[x*4+1], d[x*4+2] = s[x*4+2], s[x*4+1], s[x*4] // RGBA -> BGRA
				}
			}
		}
	}
	h := viewer.hwnd
	viewer.mu.Unlock()
	if !ok {
		keyReqMu.Lock()
		if time.Since(keyReqAt) > time.Second {
			keyReqAt = time.Now()
			sendCtl("KEYFRAME")
		}
		keyReqMu.Unlock()
		return false
	}
	framesRecv.Add(1)
	if h != 0 {
		invalidateRect.Call(h, 0, 0)
	}
	return true
}
