package main

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// 本文件提供仅测试使用的有界 PE 资源读取与图标核验辅助。
// 使用 Go 标准库 debug/pe + 受限资源目录解析，不新增业务依赖。

// --- 资源类型与解析结果 ---

const (
	rtIcon      = 3
	rtVersion   = 16
	rtGroupIcon = 14
	rtManifest  = 24
)

// groupIconEntry 是 GRPICON 目录中的一项；宽 / 高为 0 表示 256。
type groupIconEntry struct {
	ID     uint16
	Width  uint8
	Height uint8
}

// rsrcResources 是单个 .rsrc 节解析出的图标、图标组与元数据存在性。
type rsrcResources struct {
	images      map[uint16][]byte
	group       []groupIconEntry
	hasManifest bool
	hasVersion  bool
}

// winResources 汇总 PE 头信息与资源节解析结果。
type winResources struct {
	machine   uint16
	subsystem uint16
	hasRsrc   bool
	rsrcResources
}

func readU16(b []byte, off int) (uint16, error) {
	if off < 0 || off+2 > len(b) {
		return 0, fmt.Errorf("读取 uint16 越界：off=%d len=%d", off, len(b))
	}
	return binary.LittleEndian.Uint16(b[off:]), nil
}

func readU32(b []byte, off int) (uint32, error) {
	if off < 0 || off+4 > len(b) {
		return 0, fmt.Errorf("读取 uint32 越界：off=%d len=%d", off, len(b))
	}
	return binary.LittleEndian.Uint32(b[off:]), nil
}

type dirEntry struct {
	id      uint16
	isDir   bool
	subOff  uint32
	dataOff uint32
}

// parseDirectory 有界解析一个 IMAGE_RESOURCE_DIRECTORY；visited 防止目录环。
func parseDirectory(data []byte, off uint32, depth int, visited map[uint32]bool) ([]dirEntry, error) {
	if depth > 3 {
		return nil, fmt.Errorf("资源目录层数超过 3：off=%d", off)
	}
	if visited[off] {
		return nil, fmt.Errorf("资源目录存在环：off=%d", off)
	}
	visited[off] = true
	defer delete(visited, off)

	base := int(off)
	if base < 0 || base+16 > len(data) {
		return nil, fmt.Errorf("资源目录头越界：off=%d len=%d", off, len(data))
	}
	named, err := readU16(data, base+12)
	if err != nil {
		return nil, err
	}
	ids, err := readU16(data, base+14)
	if err != nil {
		return nil, err
	}
	total := int(named) + int(ids)
	entries := make([]dirEntry, 0, total)
	for i := 0; i < total; i++ {
		eo := base + 16 + i*8
		if eo+8 > len(data) {
			return nil, fmt.Errorf("资源目录条目越界：index=%d len=%d", i, len(data))
		}
		rawID := binary.LittleEndian.Uint32(data[eo:])
		raw := binary.LittleEndian.Uint32(data[eo+4:])
		e := dirEntry{id: uint16(rawID)}
		if raw&0x80000000 != 0 {
			e.isDir = true
			e.subOff = raw & 0x7FFFFFFF
		} else {
			e.dataOff = raw
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// dataEntryBytes 解析 IMAGE_RESOURCE_DATA_ENTRY，把其 RVA 转成节内偏移并做边界检查。
// baseVA 是 .rsrc 节相对镜像的虚拟地址。
func dataEntryBytes(data []byte, baseVA, dataOff uint32) ([]byte, error) {
	off := int(dataOff)
	if off < 0 || off+16 > len(data) {
		return nil, fmt.Errorf("资源数据条目越界：off=%d len=%d", dataOff, len(data))
	}
	rva := binary.LittleEndian.Uint32(data[off:])
	size := binary.LittleEndian.Uint32(data[off+4:])
	if rva < baseVA {
		return nil, fmt.Errorf("资源数据 RVA 0x%x 早于节基址 0x%x", rva, baseVA)
	}
	rel := int(rva - baseVA)
	if rel < 0 || uint64(rel)+uint64(size) > uint64(len(data)) {
		return nil, fmt.Errorf("资源数据越界：rel=%d size=%d len=%d", rel, size, len(data))
	}
	out := make([]byte, size)
	copy(out, data[rel:rel+int(size)])
	return out, nil
}

func parseGroupIcon(b []byte) ([]groupIconEntry, error) {
	if len(b) < 6 {
		return nil, fmt.Errorf("GRPICON 头过短：%d", len(b))
	}
	count, err := readU16(b, 4)
	if err != nil {
		return nil, err
	}
	if 6+int(count)*14 > len(b) {
		return nil, fmt.Errorf("GRPICON 条目越界：count=%d len=%d", count, len(b))
	}
	out := make([]groupIconEntry, 0, count)
	for i := 0; i < int(count); i++ {
		off := 6 + i*14
		id, err := readU16(b, off+12)
		if err != nil {
			return nil, err
		}
		out = append(out, groupIconEntry{ID: id, Width: b[off], Height: b[off+1]})
	}
	return out, nil
}

// parseRSRC 解析 .rsrc 节内容；仅处理 ID 型条目，忽略命名条目。
func parseRSRC(data []byte, baseVA uint32) (*rsrcResources, error) {
	res := &rsrcResources{images: map[uint16][]byte{}}
	types, err := parseDirectory(data, 0, 0, map[uint32]bool{})
	if err != nil {
		return nil, err
	}
	for _, te := range types {
		if !te.isDir {
			continue
		}
		names, err := parseDirectory(data, te.subOff, 1, map[uint32]bool{})
		if err != nil {
			return nil, err
		}
		for _, ne := range names {
			if !ne.isDir {
				continue
			}
			langs, err := parseDirectory(data, ne.subOff, 2, map[uint32]bool{})
			if err != nil {
				return nil, err
			}
			for _, le := range langs {
				if le.isDir {
					continue
				}
				blob, err := dataEntryBytes(data, baseVA, le.dataOff)
				if err != nil {
					return nil, err
				}
				switch te.id {
				case rtIcon:
					res.images[ne.id] = blob
				case rtGroupIcon:
					entries, err := parseGroupIcon(blob)
					if err != nil {
						return nil, err
					}
					res.group = append(res.group, entries...)
				case rtManifest:
					res.hasManifest = true
				case rtVersion:
					res.hasVersion = true
				}
			}
		}
	}
	return res, nil
}

// readWindowsResources 读取 exe 的 Machine / Subsystem 与 .rsrc 节。
func readWindowsResources(path string) (*winResources, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	pf, err := pe.NewFile(f)
	if err != nil {
		return nil, fmt.Errorf("解析 PE 失败：%w", err)
	}
	defer pf.Close()
	res := &winResources{machine: pf.FileHeader.Machine, rsrcResources: rsrcResources{images: map[uint16][]byte{}}}
	switch oh := pf.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		res.subsystem = oh.Subsystem
	case *pe.OptionalHeader32:
		res.subsystem = oh.Subsystem
	}
	for _, s := range pf.Sections {
		if s.Name != ".rsrc" {
			continue
		}
		res.hasRsrc = true
		data, err := s.Data()
		if err != nil {
			return nil, fmt.Errorf("读取 .rsrc 失败：%w", err)
		}
		parsed, err := parseRSRC(data, s.VirtualAddress)
		if err != nil {
			return nil, fmt.Errorf("解析 .rsrc 失败：%w", err)
		}
		res.images = parsed.images
		res.group = parsed.group
		res.hasManifest = parsed.hasManifest
		res.hasVersion = parsed.hasVersion
	}
	return res, nil
}

// --- 图案核验 ---

const (
	classTransparent = iota
	classBg
	classWhite
	classMask
)

var expectedIconSizes = []int{16, 32, 48, 64, 128, 256}

// verifyWindowsIcon 核验 Windows amd64 GUI、资源树、六档图标与当前源图的关联。
func verifyWindowsIcon(res *winResources, sourcePNG []byte) error {
	if res.machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return fmt.Errorf("Machine=0x%x 不是 amd64(0x%x)", res.machine, pe.IMAGE_FILE_MACHINE_AMD64)
	}
	if res.subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI {
		return fmt.Errorf("Subsystem=%d 不是 Windows GUI(%d)", res.subsystem, pe.IMAGE_SUBSYSTEM_WINDOWS_GUI)
	}
	if !res.hasRsrc {
		return fmt.Errorf("缺少 .rsrc 节")
	}
	if !res.hasManifest {
		return fmt.Errorf("缺少 RT_MANIFEST 资源")
	}
	if !res.hasVersion {
		return fmt.Errorf("缺少 RT_VERSION 资源")
	}

	bySize := map[int]image.Image{}
	present := map[int]bool{}
	for _, e := range res.group {
		w := int(e.Width)
		if w == 0 {
			w = 256
		}
		h := int(e.Height)
		if h == 0 {
			h = 256
		}
		if w != h {
			return fmt.Errorf("图标组尺寸非方形：%dx%d", w, h)
		}
		blob, ok := res.images[e.ID]
		if !ok {
			return fmt.Errorf("图标组引用不存在的 RT_ICON id=%d", e.ID)
		}
		img, err := png.Decode(bytes.NewReader(blob))
		if err != nil {
			return fmt.Errorf("RT_ICON id=%d 解码失败：%w", e.ID, err)
		}
		if got := img.Bounds().Dx(); got != w {
			return fmt.Errorf("RT_ICON id=%d 实际 %dpx 与组声明 %dpx 不符", e.ID, got, w)
		}
		if err := checkIconStructure(img, w); err != nil {
			return fmt.Errorf("%dpx 图标结构异常：%w", w, err)
		}
		bySize[w] = img
		present[w] = true
	}
	for _, s := range expectedIconSizes {
		if !present[s] {
			return fmt.Errorf("缺少 %dpx 图标资源", s)
		}
	}

	src, err := png.Decode(bytes.NewReader(sourcePNG))
	if err != nil {
		return fmt.Errorf("源图解码失败：%w", err)
	}
	icon256, ok := bySize[256]
	if !ok {
		return fmt.Errorf("缺少 256px 图标，无法与源图比对")
	}
	ratio := signatureMatch(src, icon256, 32)
	if ratio < 0.9 {
		return fmt.Errorf("256px 图标与当前源图不匹配（匹配率 %.2f），可能仍是旧图案", ratio)
	}
	return nil
}

// checkIconStructure 检查透明角、文档主体与遮罩分离。
func checkIconStructure(img image.Image, size int) error {
	b := img.Bounds()
	if c := color.NRGBAModel.Convert(img.At(b.Min.X, b.Min.Y)).(color.NRGBA); c.A >= 128 {
		return fmt.Errorf("圆角外左上角不透明 a=%d", c.A)
	}
	if !hasClass(img, classWhite) {
		return fmt.Errorf("缺少白色文档主体")
	}
	if size >= 32 {
		runs := darkRowRuns(img)
		thick := 0
		for _, h := range runs {
			if h >= 1 {
				thick++
			}
		}
		if thick < 2 {
			return fmt.Errorf("两条遮罩未分离，行带=%v", runs)
		}
	} else if !hasClass(img, classMask) {
		return fmt.Errorf("缺少深蓝遮罩像素")
	}
	return nil
}

func hasClass(img image.Image, class int) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if classify(img.At(x, y)) == class {
				return true
			}
		}
	}
	return false
}

func darkRowRuns(img image.Image) []int {
	b := img.Bounds()
	var runs []int
	current := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		rowDark := false
		for x := b.Min.X; x < b.Max.X; x++ {
			if classify(img.At(x, y)) == classMask {
				rowDark = true
				break
			}
		}
		if rowDark {
			current++
		} else if current > 0 {
			runs = append(runs, current)
			current = 0
		}
	}
	if current > 0 {
		runs = append(runs, current)
	}
	return runs
}

func classify(c color.Color) int {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	if n.A < 128 {
		return classTransparent
	}
	candidates := []struct {
		class   int
		r, g, b uint8
	}{
		{classBg, 0x25, 0x63, 0xEB},
		{classWhite, 0xFF, 0xFF, 0xFF},
		{classMask, 0x16, 0x3B, 0x80},
	}
	best, bestDist := classBg, 1<<30
	for _, cand := range candidates {
		d := sqDiff(n.R, cand.r) + sqDiff(n.G, cand.g) + sqDiff(n.B, cand.b)
		if d < bestDist {
			best, bestDist = cand.class, d
		}
	}
	return best
}

func sqDiff(a, b uint8) int {
	d := int(a) - int(b)
	return d * d
}

// signatureMatch 以网格中心采样比较两张图，返回分类相同的比例。
func signatureMatch(a, b image.Image, grid int) float64 {
	if grid <= 0 {
		grid = 32
	}
	matched := 0
	total := grid * grid
	for j := 0; j < grid; j++ {
		for i := 0; i < grid; i++ {
			ax := a.Bounds().Min.X + (2*i+1)*a.Bounds().Dx()/(2*grid)
			ay := a.Bounds().Min.Y + (2*j+1)*a.Bounds().Dy()/(2*grid)
			bx := b.Bounds().Min.X + (2*i+1)*b.Bounds().Dx()/(2*grid)
			by := b.Bounds().Min.Y + (2*j+1)*b.Bounds().Dy()/(2*grid)
			if classify(a.At(ax, ay)) == classify(b.At(bx, by)) {
				matched++
			}
		}
	}
	return float64(matched) / float64(total)
}

// --- 合成 .rsrc 构造（测试用） ---

type rsrcEnt struct {
	id      uint16
	isDir   bool
	sub     int
	dataOff int
}

type rsrcBuilder struct{ buf []byte }

func (b *rsrcBuilder) alloc(n int) int {
	off := len(b.buf)
	b.buf = append(b.buf, make([]byte, n)...)
	return off
}

func (b *rsrcBuilder) dir(entries []rsrcEnt) int {
	off := b.reserveDir(len(entries))
	b.fillDir(off, entries)
	return off
}

func (b *rsrcBuilder) reserveDir(n int) int {
	return b.alloc(16 + 8*n)
}

func (b *rsrcBuilder) fillDir(off int, entries []rsrcEnt) {
	binary.LittleEndian.PutUint16(b.buf[off+14:], uint16(len(entries)))
	for i, e := range entries {
		eo := off + 16 + i*8
		binary.LittleEndian.PutUint32(b.buf[eo:], uint32(e.id))
		if e.isDir {
			binary.LittleEndian.PutUint32(b.buf[eo+4:], uint32(e.sub)|0x80000000)
		} else {
			binary.LittleEndian.PutUint32(b.buf[eo+4:], uint32(e.dataOff))
		}
	}
}

func (b *rsrcBuilder) data(blob []byte) int {
	raw := b.alloc(len(blob))
	copy(b.buf[raw:], blob)
	de := b.alloc(16)
	binary.LittleEndian.PutUint32(b.buf[de:], uint32(raw))
	binary.LittleEndian.PutUint32(b.buf[de+4:], uint32(len(blob)))
	return de
}

// leafDir 生成“语言层目录 → 数据条目”。
func (b *rsrcBuilder) leafDir(blob []byte) int {
	return b.dir([]rsrcEnt{{id: 0x0409, dataOff: b.data(blob)}})
}

func groupBlob(sizes []int, ids []uint16) []byte {
	buf := make([]byte, 6+14*len(sizes))
	binary.LittleEndian.PutUint16(buf[0:], 0)
	binary.LittleEndian.PutUint16(buf[2:], 1)
	binary.LittleEndian.PutUint16(buf[4:], uint16(len(sizes)))
	for i, s := range sizes {
		off := 6 + i*14
		w := byte(s)
		if s == 256 {
			w = 0
		}
		buf[off] = w
		buf[off+1] = w
		binary.LittleEndian.PutUint16(buf[off+4:], 1)
		binary.LittleEndian.PutUint16(buf[off+6:], 32)
		binary.LittleEndian.PutUint16(buf[off+12:], ids[i])
	}
	return buf
}

// --- 测试 ---

func TestParseRSRCValid(t *testing.T) {
	b := &rsrcBuilder{}
	root := b.reserveDir(4) // 根目录始终位于节偏移 0
	icon1 := b.leafDir([]byte("icon-one"))
	icon2 := b.leafDir([]byte("icon-two"))
	group := b.leafDir(groupBlob([]int{16, 32}, []uint16{1, 2}))
	manifest := b.leafDir([]byte("<manifest/>"))
	version := b.leafDir([]byte("version"))
	b.fillDir(root, []rsrcEnt{
		{id: rtIcon, isDir: true, sub: b.dir([]rsrcEnt{
			{id: 1, isDir: true, sub: icon1},
			{id: 2, isDir: true, sub: icon2},
		})},
		{id: rtGroupIcon, isDir: true, sub: b.dir([]rsrcEnt{{id: 1, isDir: true, sub: group}})},
		{id: rtManifest, isDir: true, sub: b.dir([]rsrcEnt{{id: 1, isDir: true, sub: manifest}})},
		{id: rtVersion, isDir: true, sub: b.dir([]rsrcEnt{{id: 1, isDir: true, sub: version}})},
	})

	res, err := parseRSRC(b.buf, 0)
	if err != nil {
		t.Fatalf("有效资源解析失败：%v", err)
	}
	if len(res.images) != 2 || string(res.images[1]) != "icon-one" || string(res.images[2]) != "icon-two" {
		t.Errorf("RT_ICON 解析错误：%v", res.images)
	}
	if len(res.group) != 2 || res.group[0].ID != 1 || res.group[1].ID != 2 {
		t.Errorf("GRPICON 解析错误：%+v", res.group)
	}
	if !res.hasManifest || !res.hasVersion {
		t.Errorf("manifest/version 未识别：%+v", res)
	}
}

func TestParseRSRCTruncated(t *testing.T) {
	inputs := [][]byte{
		nil,
		{0x00},
		make([]byte, 8),
		make([]byte, 15),
	}
	for i, in := range inputs {
		if _, err := parseRSRC(in, 0); err == nil {
			t.Errorf("截断输入 #%d 应报错", i)
		}
	}
}

func TestParseRSRCOutOfRangeSubdir(t *testing.T) {
	buf := make([]byte, 24)
	// 一个 ID 条目，子目录偏移指向节外。
	binary.LittleEndian.PutUint16(buf[14:], 1)
	binary.LittleEndian.PutUint32(buf[16:], 3)
	binary.LittleEndian.PutUint32(buf[20:], 0x80000000|0x00FFFFFF)
	if _, err := parseRSRC(buf, 0); err == nil {
		t.Fatal("越界子目录应报错")
	}
}

func TestParseRSRCOutOfRangeData(t *testing.T) {
	b := &rsrcBuilder{}
	root := b.reserveDir(1)
	// 手工数据条目：RVA/大小远超节内容。
	de := b.alloc(16)
	binary.LittleEndian.PutUint32(b.buf[de:], 0xFFFF)
	binary.LittleEndian.PutUint32(b.buf[de+4:], 0xFFFF)
	icon := b.dir([]rsrcEnt{{id: 0x0409, dataOff: de}})
	b.fillDir(root, []rsrcEnt{{id: rtIcon, isDir: true, sub: b.dir([]rsrcEnt{{id: 1, isDir: true, sub: icon}})}})
	if _, err := parseRSRC(b.buf, 0); err == nil {
		t.Fatal("越界资源数据应报错")
	}
}

func TestVerifyIconModelRejectsOldPatternMissingAndBadRef(t *testing.T) {
	sourcePNG := readRepoFile(t, "build", "appicon.png")
	source, err := png.Decode(bytes.NewReader(sourcePNG))
	if err != nil {
		t.Fatal(err)
	}

	// 有效：由当前源图缩放出的六档图标。
	valid := &winResources{
		machine:   pe.IMAGE_FILE_MACHINE_AMD64,
		subsystem: pe.IMAGE_SUBSYSTEM_WINDOWS_GUI,
		hasRsrc:   true,
		rsrcResources: rsrcResources{
			images:      map[uint16][]byte{},
			hasManifest: true,
			hasVersion:  true,
		},
	}
	for i, s := range expectedIconSizes {
		id := uint16(i + 1)
		valid.images[id] = encodeScaledPNG(t, source, s)
		valid.group = append(valid.group, groupIconEntry{ID: id, Width: dimByte(s), Height: dimByte(s)})
	}
	if err := verifyWindowsIcon(valid, sourcePNG); err != nil {
		t.Fatalf("有效资源应通过：%v", err)
	}

	// 旧三白条图案：与当前源图不匹配。
	old := cloneWinResources(valid)
	oldPattern := drawThreeBars(256)
	for id := range old.images {
		old.images[id] = encodeScaledPNG(t, oldPattern, sizeOfGroup(old.group, id))
	}
	if err := verifyWindowsIcon(old, sourcePNG); err == nil {
		t.Fatal("旧图案资源不应通过")
	}

	// 缺档：移除 256。
	missing := cloneWinResources(valid)
	for i, e := range missing.group {
		if e.Width == 0 || e.Width == 255 {
			delete(missing.images, e.ID)
			missing.group = append(missing.group[:i], missing.group[i+1:]...)
			break
		}
	}
	if err := verifyWindowsIcon(missing, sourcePNG); err == nil {
		t.Fatal("缺档资源不应通过")
	}

	// 错引用：图标组指向不存在的 RT_ICON。
	badRef := cloneWinResources(valid)
	badRef.group[0].ID = 999
	if err := verifyWindowsIcon(badRef, sourcePNG); err == nil {
		t.Fatal("错引用资源不应通过")
	}
}

func TestCrossBuiltWindowsExeResources(t *testing.T) {
	exe := filepath.Join("..", "..", "dist", "windows-amd64", "data-masking.exe")
	if _, err := os.Stat(exe); err != nil {
		t.Skip("dist/windows-amd64/data-masking.exe 不存在；先执行 go run ./scripts/build -platform windows/amd64")
	}
	sourcePNG := readRepoFile(t, "build", "appicon.png")
	res, err := readWindowsResources(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyWindowsIcon(res, sourcePNG); err != nil {
		t.Fatalf("exe 图标资源核验失败：%v", err)
	}
	if len(res.images) != 6 {
		t.Errorf("应有 6 档 RT_ICON，实际 %d", len(res.images))
	}
	var sizes []int
	for _, e := range res.group {
		if e.Width == 0 {
			sizes = append(sizes, 256)
			continue
		}
		sizes = append(sizes, int(e.Width))
	}
	src, err := png.Decode(bytes.NewReader(sourcePNG))
	if err != nil {
		t.Fatal(err)
	}
	var icon256 image.Image
	for _, e := range res.group {
		if e.Width == 0 {
			icon256, err = png.Decode(bytes.NewReader(res.images[e.ID]))
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	ratio := signatureMatch(src, icon256, 32)
	t.Logf("Machine=0x%x Subsystem=%d hasRsrc=%v hasManifest=%v hasVersion=%v", res.machine, res.subsystem, res.hasRsrc, res.hasManifest, res.hasVersion)
	t.Logf("RT_ICON=%d 图标组尺寸=%v 与源图匹配率=%.3f", len(res.images), sizes, ratio)
}

// --- 核验辅助 ---

func cloneWinResources(in *winResources) *winResources {
	out := &winResources{
		machine:   in.machine,
		subsystem: in.subsystem,
		hasRsrc:   in.hasRsrc,
		rsrcResources: rsrcResources{
			images:      map[uint16][]byte{},
			group:       append([]groupIconEntry(nil), in.group...),
			hasManifest: in.hasManifest,
			hasVersion:  in.hasVersion,
		},
	}
	for k, v := range in.images {
		out.images[k] = v
	}
	return out
}

func sizeOfGroup(group []groupIconEntry, id uint16) int {
	for _, e := range group {
		if e.ID == id {
			if e.Width == 0 {
				return 256
			}
			return int(e.Width)
		}
	}
	return 256
}

func dimByte(size int) uint8 {
	if size >= 256 {
		return 0
	}
	return uint8(size)
}

func readRepoFile(t *testing.T, elems ...string) []byte {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, elems...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	return data
}

// encodeScaledPNG 用最近邻采样把 src 缩放到 size 并编码为 PNG。
func encodeScaledPNG(t *testing.T, src image.Image, size int) []byte {
	t.Helper()
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	sb := src.Bounds()
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			sx := sb.Min.X + x*sb.Dx()/size
			sy := sb.Min.Y + y*sb.Dy()/size
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// drawThreeBars 生成旧的三条等长白条图案，用于验证旧图案不能误通过。
func drawThreeBars(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{0x25, 0x63, 0xEB, 0xFF}
	white := color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, bg)
		}
	}
	barH := size / 12
	gap := size / 10
	startY := (size - (3*barH + 2*gap)) / 2
	for i := 0; i < 3; i++ {
		y0 := startY + i*(barH+gap)
		for y := y0; y < y0+barH; y++ {
			for x := size / 5; x < size*4/5; x++ {
				img.Set(x, y, white)
			}
		}
	}
	return img
}
