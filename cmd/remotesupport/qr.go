//go:build windows

package main

// Self-contained QR Code encoder (byte mode). Supports versions 1..10 with
// error-correction level L or M, full 8-mask penalty selection. No deps.
// Returns a square [][]bool module matrix (true = dark).

// ---- Galois field GF(256), primitive 0x11d ----
var gfExp [512]int
var gfLog [256]int

func gfInit() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = x
		gfLog[x] = i
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b int) int {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[gfLog[a]+gfLog[b]]
}

// rsGenPoly returns the generator polynomial for n ecc codewords.
func rsGenPoly(n int) []int {
	poly := []int{1}
	for i := 0; i < n; i++ {
		next := make([]int, len(poly)+1)
		for j := 0; j < len(poly); j++ {
			next[j] ^= poly[j]
			next[j+1] ^= gfMul(poly[j], gfExp[i])
		}
		poly = next
	}
	return poly
}

func rsEncode(data []int, nec int) []int {
	gen := rsGenPoly(nec)
	res := make([]int, len(data)+nec)
	copy(res, data)
	for i := 0; i < len(data); i++ {
		coef := res[i]
		if coef != 0 {
			for j := 0; j < len(gen); j++ {
				res[i+j] ^= gfMul(gen[j], coef)
			}
		}
	}
	return res[len(data):]
}

// ---- Version/EC block tables (L and M), versions 1..10 ----
// For each version+level: total data codewords, ec codewords per block,
// group1 blocks, group1 data cw per block, group2 blocks, group2 data cw per block.
type ecBlockInfo struct {
	ecPerBlock int
	g1Blocks   int
	g1Data     int
	g2Blocks   int
	g2Data     int
}

// index [version-1][level] where level 0=L,1=M
var ecTable = map[int][2]ecBlockInfo{
	1:  {{7, 1, 19, 0, 0}, {10, 1, 16, 0, 0}},
	2:  {{10, 1, 34, 0, 0}, {16, 1, 28, 0, 0}},
	3:  {{15, 1, 55, 0, 0}, {26, 1, 44, 0, 0}},
	4:  {{20, 1, 80, 0, 0}, {18, 2, 32, 0, 0}},
	5:  {{26, 1, 108, 0, 0}, {24, 2, 43, 0, 0}},
	6:  {{18, 2, 68, 0, 0}, {16, 4, 27, 0, 0}},
	7:  {{20, 2, 78, 0, 0}, {18, 4, 31, 0, 0}},
	8:  {{24, 2, 97, 0, 0}, {22, 2, 38, 2, 39}},
	9:  {{30, 2, 116, 0, 0}, {22, 3, 36, 2, 37}},
	10: {{18, 2, 68, 2, 69}, {26, 4, 43, 1, 44}},
}

func dataCapacityBytes(version, level int) int {
	info := ecTable[version][level]
	totalData := info.g1Blocks*info.g1Data + info.g2Blocks*info.g2Data
	// subtract mode(4 bits)+count indicator, computed by caller; here return raw data codewords
	return totalData
}

// alignment pattern center coordinates per version (1..10)
var alignPos = map[int][]int{
	1:  {},
	2:  {6, 18},
	3:  {6, 22},
	4:  {6, 26},
	5:  {6, 30},
	6:  {6, 34},
	7:  {6, 22, 38},
	8:  {6, 24, 42},
	9:  {6, 26, 46},
	10: {6, 28, 50},
}

func charCountBits(version int) int {
	if version <= 9 {
		return 8
	}
	return 16
}

// buildData creates the full codeword stream (data+ecc interleaved) for text.
func buildData(text string, version, level int) []int {
	info := ecTable[version][level]
	totalDataCW := info.g1Blocks*info.g1Data + info.g2Blocks*info.g2Data

	// bit buffer
	var bits []int
	put := func(val, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, (val>>i)&1)
		}
	}
	put(0b0100, 4) // byte mode
	put(len(text), charCountBits(version))
	for i := 0; i < len(text); i++ {
		put(int(text[i]), 8)
	}
	// terminator
	cap := totalDataCW * 8
	for len(bits) < cap && len(bits) < len(bits)+4 {
		if len(bits) >= cap {
			break
		}
		term := 4
		if cap-len(bits) < 4 {
			term = cap - len(bits)
		}
		for i := 0; i < term; i++ {
			bits = append(bits, 0)
		}
		break
	}
	// pad to byte
	for len(bits)%8 != 0 {
		bits = append(bits, 0)
	}
	// data codewords
	var dataCW []int
	for i := 0; i < len(bits); i += 8 {
		b := 0
		for j := 0; j < 8; j++ {
			b = (b << 1) | bits[i+j]
		}
		dataCW = append(dataCW, b)
	}
	// pad bytes
	pad := []int{0xEC, 0x11}
	pi := 0
	for len(dataCW) < totalDataCW {
		dataCW = append(dataCW, pad[pi%2])
		pi++
	}

	// split into blocks
	var blocks [][]int
	var eccBlocks [][]int
	idx := 0
	for b := 0; b < info.g1Blocks; b++ {
		blk := dataCW[idx : idx+info.g1Data]
		idx += info.g1Data
		blocks = append(blocks, blk)
		eccBlocks = append(eccBlocks, rsEncode(blk, info.ecPerBlock))
	}
	for b := 0; b < info.g2Blocks; b++ {
		blk := dataCW[idx : idx+info.g2Data]
		idx += info.g2Data
		blocks = append(blocks, blk)
		eccBlocks = append(eccBlocks, rsEncode(blk, info.ecPerBlock))
	}

	// interleave data
	var out []int
	maxData := info.g1Data
	if info.g2Data > maxData {
		maxData = info.g2Data
	}
	for i := 0; i < maxData; i++ {
		for _, blk := range blocks {
			if i < len(blk) {
				out = append(out, blk[i])
			}
		}
	}
	// interleave ecc
	for i := 0; i < info.ecPerBlock; i++ {
		for _, blk := range eccBlocks {
			if i < len(blk) {
				out = append(out, blk[i])
			}
		}
	}
	return out
}

// ---- matrix construction ----
type matrix struct {
	size int
	mods [][]int8 // -1 unset, 0 light, 1 dark
	fn   [][]bool // function module (reserved)
}

func newMatrix(size int) *matrix {
	m := &matrix{size: size}
	m.mods = make([][]int8, size)
	m.fn = make([][]bool, size)
	for i := range m.mods {
		m.mods[i] = make([]int8, size)
		m.fn[i] = make([]bool, size)
		for j := range m.mods[i] {
			m.mods[i][j] = -1
		}
	}
	return m
}

func (m *matrix) set(r, c int, dark bool, fn bool) {
	if dark {
		m.mods[r][c] = 1
	} else {
		m.mods[r][c] = 0
	}
	if fn {
		m.fn[r][c] = true
	}
}

func placeFinder(m *matrix, r, c int) {
	for dr := -1; dr <= 7; dr++ {
		for dc := -1; dc <= 7; dc++ {
			rr, cc := r+dr, c+dc
			if rr < 0 || rr >= m.size || cc < 0 || cc >= m.size {
				continue
			}
			dark := false
			if dr >= 0 && dr <= 6 && dc >= 0 && dc <= 6 {
				if dr == 0 || dr == 6 || dc == 0 || dc == 6 {
					dark = true
				} else if dr >= 2 && dr <= 4 && dc >= 2 && dc <= 4 {
					dark = true
				}
			}
			m.set(rr, cc, dark, true)
		}
	}
}

func placeAlignment(m *matrix, version int) {
	pos := alignPos[version]
	for _, r := range pos {
		for _, c := range pos {
			// skip if overlapping finder
			if (r == 6 && c == 6) || (r == 6 && c == pos[len(pos)-1]) || (r == pos[len(pos)-1] && c == 6) {
				// still may collide with finders at corners; check function
			}
			if m.fn[r][c] {
				continue
			}
			for dr := -2; dr <= 2; dr++ {
				for dc := -2; dc <= 2; dc++ {
					dark := dr == -2 || dr == 2 || dc == -2 || dc == 2 || (dr == 0 && dc == 0)
					m.set(r+dr, c+dc, dark, true)
				}
			}
		}
	}
}

func placeTiming(m *matrix) {
	for i := 8; i < m.size-8; i++ {
		dark := i%2 == 0
		if !m.fn[6][i] {
			m.set(6, i, dark, true)
		}
		if !m.fn[i][6] {
			m.set(i, 6, dark, true)
		}
	}
}

func reserveFormat(m *matrix, version int) {
	// format info areas around finders (15 bits)
	for i := 0; i <= 8; i++ {
		if !m.fn[8][i] {
			m.set(8, i, false, true)
		}
		if !m.fn[i][8] {
			m.set(i, 8, false, true)
		}
	}
	for i := 0; i < 8; i++ {
		m.set(8, m.size-1-i, false, true)
		m.set(m.size-1-i, 8, false, true)
	}
	// dark module
	m.set(m.size-8, 8, true, true)
	// version info (v>=7): two 3x6 blocks
	if version >= 7 {
		for i := 0; i < 18; i++ {
			r := i / 3
			c := i % 3
			m.set(m.size-11+c, r, false, true)
			m.set(r, m.size-11+c, false, true)
		}
	}
}

func placeData(m *matrix, data []int) {
	size := m.size
	bitIdx := 0
	getBit := func() int {
		if bitIdx >= len(data)*8 {
			return 0
		}
		b := data[bitIdx/8]
		bit := (b >> (7 - bitIdx%8)) & 1
		bitIdx++
		return bit
	}
	col := size - 1
	upward := true
	for col > 0 {
		if col == 6 {
			col-- // skip timing column
		}
		for i := 0; i < size; i++ {
			var row int
			if upward {
				row = size - 1 - i
			} else {
				row = i
			}
			for c := 0; c < 2; c++ {
				cc := col - c
				if m.mods[row][cc] == -1 && !m.fn[row][cc] {
					m.mods[row][cc] = int8(getBit())
				}
			}
		}
		upward = !upward
		col -= 2
	}
}

func applyMask(m *matrix, mask int) *matrix {
	out := newMatrix(m.size)
	for r := 0; r < m.size; r++ {
		for c := 0; c < m.size; c++ {
			out.fn[r][c] = m.fn[r][c]
			v := m.mods[r][c]
			if !m.fn[r][c] && v != -1 {
				var flip bool
				switch mask {
				case 0:
					flip = (r+c)%2 == 0
				case 1:
					flip = r%2 == 0
				case 2:
					flip = c%3 == 0
				case 3:
					flip = (r+c)%3 == 0
				case 4:
					flip = (r/2+c/3)%2 == 0
				case 5:
					flip = (r*c)%2+(r*c)%3 == 0
				case 6:
					flip = ((r*c)%2+(r*c)%3)%2 == 0
				case 7:
					flip = ((r+c)%2+(r*c)%3)%2 == 0
				}
				if flip {
					v ^= 1
				}
			}
			out.mods[r][c] = v
		}
	}
	return out
}

var ecLevelBits = map[int]int{0: 0b01, 1: 0b00} // L=01, M=00 (per spec)

func placeFormat(m *matrix, level, mask int) {
	data := (ecLevelBits[level] << 3) | mask
	// BCH(15,5)
	rem := data
	for i := 0; i < 10; i++ {
		rem <<= 1
	}
	g := 0b10100110111
	r := data << 10
	for i := 14; i >= 10; i-- {
		if (r>>i)&1 == 1 {
			r ^= g << (i - 10)
		}
	}
	bits := ((data << 10) | r) ^ 0b101010000010010
	_ = rem
	// place 15 bits
	// around top-left
	for i := 0; i <= 5; i++ {
		setModDark(m, 8, i, (bits>>i)&1 == 1)
	}
	setModDark(m, 8, 7, (bits>>6)&1 == 1)
	setModDark(m, 8, 8, (bits>>7)&1 == 1)
	setModDark(m, 7, 8, (bits>>8)&1 == 1)
	for i := 9; i <= 14; i++ {
		setModDark(m, 14-i, 8, (bits>>i)&1 == 1)
	}
	// around top-right / bottom-left
	for i := 0; i <= 7; i++ {
		setModDark(m, 8, m.size-1-i, (bits>>i)&1 == 1)
	}
	for i := 8; i <= 14; i++ {
		setModDark(m, m.size-15+i, 8, (bits>>i)&1 == 1)
	}
}

func placeVersion(m *matrix, version int) {
	if version < 7 {
		return
	}
	// BCH(18,6) version info
	g := 0b1111100100101
	d := version << 12
	r := d
	for i := 17; i >= 12; i-- {
		if (r>>i)&1 == 1 {
			r ^= g << (i - 12)
		}
	}
	bits := (version << 12) | (r & 0xFFF)
	for i := 0; i < 18; i++ {
		bit := (bits>>i)&1 == 1
		row := i / 3
		col := i % 3
		setModDark(m, m.size-11+col, row, bit)
		setModDark(m, row, m.size-11+col, bit)
	}
}

func setModDark(m *matrix, r, c int, dark bool) {
	if dark {
		m.mods[r][c] = 1
	} else {
		m.mods[r][c] = 0
	}
}

func penalty(m *matrix) int {
	size := m.size
	score := 0
	dark := func(r, c int) bool { return m.mods[r][c] == 1 }
	// rule 1: runs
	for r := 0; r < size; r++ {
		runC := 1
		runR := 1
		for c := 1; c < size; c++ {
			if dark(r, c) == dark(r, c-1) {
				runC++
			} else {
				if runC >= 5 {
					score += 3 + (runC - 5)
				}
				runC = 1
			}
			if dark(c, r) == dark(c-1, r) {
				runR++
			} else {
				if runR >= 5 {
					score += 3 + (runR - 5)
				}
				runR = 1
			}
		}
		if runC >= 5 {
			score += 3 + (runC - 5)
		}
		if runR >= 5 {
			score += 3 + (runR - 5)
		}
	}
	// rule 2: 2x2 blocks
	for r := 0; r < size-1; r++ {
		for c := 0; c < size-1; c++ {
			if dark(r, c) == dark(r, c+1) && dark(r, c) == dark(r+1, c) && dark(r, c) == dark(r+1, c+1) {
				score += 3
			}
		}
	}
	// rule 3: finder-like patterns
	pat1 := []bool{true, false, true, true, true, false, true, false, false, false, false}
	pat2 := []bool{false, false, false, false, true, false, true, true, true, false, true}
	check := func(get func(i int) bool) {
		for start := 0; start <= size-11; start++ {
			m1, m2 := true, true
			for k := 0; k < 11; k++ {
				if get(start+k) != pat1[k] {
					m1 = false
				}
				if get(start+k) != pat2[k] {
					m2 = false
				}
			}
			if m1 || m2 {
				score += 40
			}
		}
	}
	for r := 0; r < size; r++ {
		rr := r
		check(func(i int) bool { return dark(rr, i) })
	}
	for c := 0; c < size; c++ {
		cc := c
		check(func(i int) bool { return dark(i, cc) })
	}
	// rule 4: dark ratio
	total := size * size
	darkCount := 0
	for r := 0; r < size; r++ {
		for c := 0; c < size; c++ {
			if dark(r, c) {
				darkCount++
			}
		}
	}
	pct := darkCount * 100 / total
	dev := pct - 50
	if dev < 0 {
		dev = -dev
	}
	score += (dev / 5) * 10
	return score
}

// Encode returns the module matrix for text at EC level (0=L,1=M).
func qrEncode(text string, level int) ([][]bool, int) {
	gfInit()
	// choose smallest version
	version := 0
	for v := 1; v <= 10; v++ {
		totalDataCW := dataCapacityBytes(v, level)
		// available data bits minus mode(4)+count
		avail := totalDataCW*8 - 4 - charCountBits(v)
		if len(text)*8 <= avail {
			version = v
			break
		}
	}
	if version == 0 {
		version = 10
	}
	size := 17 + version*4
	data := buildData(text, version, level)

	base := newMatrix(size)
	placeFinder(base, 0, 0)
	placeFinder(base, 0, size-7)
	placeFinder(base, size-7, 0)
	placeAlignment(base, version)
	placeTiming(base)
	reserveFormat(base, version)
	placeData(base, data)

	// try all masks, pick best
	best := -1
	bestScore := 1 << 30
	var bestM *matrix
	for mask := 0; mask < 8; mask++ {
		cand := applyMask(base, mask)
		placeFormat(cand, level, mask)
		placeVersion(cand, version)
		s := penalty(cand)
		if s < bestScore {
			bestScore = s
			best = mask
			bestM = cand
		}
	}
	_ = best
	out := make([][]bool, size)
	for r := 0; r < size; r++ {
		out[r] = make([]bool, size)
		for c := 0; c < size; c++ {
			out[r][c] = bestM.mods[r][c] == 1
		}
	}
	return out, version
}
