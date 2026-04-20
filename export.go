package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// ---------------------------------------------------------------------------
// Generic map-access helpers (mirrors Python dict.get(key, default))
// ---------------------------------------------------------------------------

func getMap(m map[string]interface{}, key string) map[string]interface{} {
	if m == nil {
		return nil
	}
	if v, ok := m[key]; ok {
		if mm, ok := v.(map[string]interface{}); ok {
			return mm
		}
	}
	return nil
}

func getMapDef(m map[string]interface{}, key string) map[string]interface{} {
	r := getMap(m, key)
	if r == nil {
		return map[string]interface{}{}
	}
	return r
}

func getFloat(m map[string]interface{}, key string, def float64) float64 {
	if m == nil {
		return def
	}
	v, ok := m[key]
	if !ok {
		return def
	}
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case json.Number:
		f, err := t.Float64()
		if err == nil {
			return f
		}
	}
	return def
}

func getInt64(m map[string]interface{}, key string, def int64) int64 {
	return int64(getFloat(m, key, float64(def)))
}

func getInt(m map[string]interface{}, key string, def int) int {
	return int(getFloat(m, key, float64(def)))
}

func getString(m map[string]interface{}, key string, def string) string {
	if m == nil {
		return def
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

func getBool(m map[string]interface{}, key string, def bool) bool {
	if m == nil {
		return def
	}
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

func getSlice(m map[string]interface{}, key string) []interface{} {
	if m == nil {
		return nil
	}
	if v, ok := m[key]; ok {
		if s, ok := v.([]interface{}); ok {
			return s
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Utility helpers
// ---------------------------------------------------------------------------

var unsafeChars = regexp.MustCompile(`[<>:"/\\|?*]`)

func sanitizeName(name string) string {
	name = unsafeChars.ReplaceAllString(name, "")
	name = strings.Trim(name, ". ")
	if name == "" {
		return "track"
	}
	return name
}

func makeEven(n int) int {
	if n%2 == 0 {
		return n
	}
	return n + 1
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func fileStem(p string) string {
	base := filepath.Base(p)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

// ---------------------------------------------------------------------------
// GPU detection
// ---------------------------------------------------------------------------

func hasNvidiaGPU() bool {
	_, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return false
	}
	cmd := exec.Command("nvidia-smi", "--query-gpu=name", "--format=csv,noheader")
	cmd.SysProcAttr = noWindow
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// ---------------------------------------------------------------------------
// ffprobe
// ---------------------------------------------------------------------------

type ProbeResult struct {
	Width    int
	Height   int
	Duration float64
}

var (
	probeCache   = make(map[string]ProbeResult)
	probeCacheMu sync.Mutex
)

func probeVideo(path string) ProbeResult {
	probeCacheMu.Lock()
	if r, ok := probeCache[path]; ok {
		probeCacheMu.Unlock()
		return r
	}
	probeCacheMu.Unlock()

	result := ProbeResult{}
	cmd := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json",
		"-show_streams", "-select_streams", "v:0", path)
	cmd.SysProcAttr = noWindow
	out, err := cmd.Output()
	if err != nil {
		probeCacheMu.Lock()
		probeCache[path] = result
		probeCacheMu.Unlock()
		return result
	}

	var data struct {
		Streams []struct {
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			Duration string `json:"duration"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &data); err != nil || len(data.Streams) == 0 {
		probeCacheMu.Lock()
		probeCache[path] = result
		probeCacheMu.Unlock()
		return result
	}

	s := data.Streams[0]
	result.Width = s.Width
	result.Height = s.Height
	fmt.Sscanf(s.Duration, "%f", &result.Duration)

	probeCacheMu.Lock()
	probeCache[path] = result
	probeCacheMu.Unlock()
	return result
}

// ---------------------------------------------------------------------------
// Video filter builder  (mirrors Python build_vf exactly)
// ---------------------------------------------------------------------------

func buildVF(
	segment map[string]interface{},
	srcW, srcH, canvasW, canvasH, outW, outH int,
) string {
	clip := getMapDef(segment, "clip")
	scaleD := getMapDef(clip, "scale")
	transformD := getMapDef(clip, "transform")
	flipD := getMapDef(clip, "flip")

	sx := getFloat(scaleD, "x", 1.0)
	sy := getFloat(scaleD, "y", 1.0)
	tx := getFloat(transformD, "x", 0.0)
	ty := getFloat(transformD, "y", 0.0)
	rotation := getFloat(clip, "rotation", 0.0)
	flipH := getBool(flipD, "horizontal", false)
	flipV := getBool(flipD, "vertical", false)
	speed := getFloat(segment, "speed", 1.0)

	hasTransform := math.Abs(sx-1.0) > 1e-6 ||
		math.Abs(sy-1.0) > 1e-6 ||
		math.Abs(tx) > 1e-4 ||
		math.Abs(ty) > 1e-4 ||
		math.Abs(rotation) > 0.01 ||
		flipH || flipV

	var filters []string

	// 1. Speed
	if math.Abs(speed-1.0) > 1e-6 {
		filters = append(filters, fmt.Sprintf("setpts=PTS/%.6f", speed))
	}

	// 2. Flip
	if flipH {
		filters = append(filters, "hflip")
	}
	if flipV {
		filters = append(filters, "vflip")
	}

	// 3. Geometric transform
	if hasTransform {
		if srcW == 0 || srcH == 0 {
			filters = append(filters,
				fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=lanczos", outW, outH),
				fmt.Sprintf("pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black", outW, outH),
			)
			return strings.Join(filters, ",")
		}

		sf := float64(outW) / float64(canvasW)
		autoFit := math.Min(float64(canvasW)/float64(srcW), float64(canvasH)/float64(srcH))

		dispWC := float64(srcW) * autoFit * sx
		dispHC := float64(srcH) * autoFit * sy

		cxC := float64(canvasW) / 2.0 * (1.0 + tx)
		cyC := float64(canvasH) / 2.0 * (1.0 - ty)

		leftC := cxC - dispWC/2.0
		topC := cyC - dispHC/2.0

		dispW := makeEven(int(dispWC * sf))
		dispH := makeEven(int(dispHC * sf))
		left := leftC * sf
		top := topC * sf

		filters = append(filters, fmt.Sprintf("scale=%d:%d:flags=lanczos", dispW, dispH))

		if math.Abs(rotation) > 0.01 {
			rad := -rotation * math.Pi / 180.0
			filters = append(filters, fmt.Sprintf("rotate=%.8f:fillcolor=black@1:ow=iw:oh=ih", rad))
		}

		leftOvf := math.Max(0, -left)
		topOvf := math.Max(0, -top)
		rightOvf := math.Max(0, left+float64(dispW)-float64(outW))
		bottomOvf := math.Max(0, top+float64(dispH)-float64(outH))

		visW := float64(dispW) - leftOvf - rightOvf
		visH := float64(dispH) - topOvf - bottomOvf

		if visW <= 0 || visH <= 0 {
			filters = append(filters, fmt.Sprintf("scale=%d:%d,nullsrc=s=%dx%d", outW, outH, outW, outH))
			return strings.Join(filters, ",")
		}

		if leftOvf > 0.5 || topOvf > 0.5 || rightOvf > 0.5 || bottomOvf > 0.5 {
			cx := int(leftOvf)
			cy := int(topOvf)
			cw := makeEven(int(visW))
			ch := makeEven(int(visH))
			filters = append(filters, fmt.Sprintf("crop=%d:%d:%d:%d", cw, ch, cx, cy))
		}

		px := int(math.Max(0, left))
		py := int(math.Max(0, top))
		if int(visW) < outW || int(visH) < outH {
			filters = append(filters, fmt.Sprintf("pad=%d:%d:%d:%d:color=black", outW, outH, px, py))
		}
	} else {
		if math.Abs(rotation) > 0.01 {
			rad := -rotation * math.Pi / 180.0
			filters = append(filters, fmt.Sprintf("rotate=%.6f:fillcolor=black@1:ow=iw:oh=ih", rad))
		}
		filters = append(filters,
			fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=lanczos", outW, outH),
			fmt.Sprintf("pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black", outW, outH),
		)
	}

	return strings.Join(filters, ",")
}

// ---------------------------------------------------------------------------
// Compound clip resolver
// ---------------------------------------------------------------------------

func findSubdraftDir(draftDir, materialName string) string {
	subdraftRoot := filepath.Join(draftDir, "subdraft")
	entries, err := os.ReadDir(subdraftRoot)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cfgPath := filepath.Join(subdraftRoot, entry.Name(), "sub_draft_config.json")
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			continue
		}
		var cfg map[string]interface{}
		if err := json.Unmarshal(data, &cfg); err != nil {
			continue
		}
		if getString(cfg, "name", "") == materialName {
			return filepath.Join(subdraftRoot, entry.Name())
		}
	}
	return ""
}

func loadInnerDraft(subdraftDir string) map[string]interface{} {
	dcPath := filepath.Join(subdraftDir, "draft_content.json")
	data, err := os.ReadFile(dcPath)
	if err != nil {
		return nil
	}
	var dc map[string]interface{}
	if err := json.Unmarshal(data, &dc); err != nil {
		return nil
	}
	materials := getMap(dc, "materials")
	if materials == nil {
		return nil
	}
	for _, d := range getSlice(materials, "drafts") {
		dm, ok := d.(map[string]interface{})
		if !ok {
			continue
		}
		inner := getMap(dm, "draft")
		if inner != nil && getSlice(inner, "tracks") != nil {
			return inner
		}
	}
	return nil
}

type InnerSeg struct {
	Seg map[string]interface{}
	Mat map[string]interface{}
}

func resolveCompoundSegments(compoundSeg, compoundMat map[string]interface{}, draftDir string, depth int) []InnerSeg {
	if depth > 10 {
		return nil
	}
	matName := getString(compoundMat, "material_name", "")
	if matName == "" {
		return nil
	}

	subDir := findSubdraftDir(draftDir, matName)
	if subDir == "" {
		return nil
	}

	inner := loadInnerDraft(subDir)
	if inner == nil {
		return nil
	}

	innerMats := make(map[string]map[string]interface{})
	innerMaterials := getMap(inner, "materials")
	if innerMaterials != nil {
		for _, v := range getSlice(innerMaterials, "videos") {
			if m, ok := v.(map[string]interface{}); ok {
				id := getString(m, "id", "")
				if id != "" {
					innerMats[id] = m
				}
			}
		}
	}

	outerSrc := getMapDef(compoundSeg, "source_timerange")
	outerStart := getInt64(outerSrc, "start", 0)
	outerEnd := outerStart + getInt64(outerSrc, "duration", 0)

	oc := getMapDef(compoundSeg, "clip")
	oSx := getFloat(getMapDef(oc, "scale"), "x", 1.0)
	oSy := getFloat(getMapDef(oc, "scale"), "y", 1.0)
	oTx := getFloat(getMapDef(oc, "transform"), "x", 0.0)
	oTy := getFloat(getMapDef(oc, "transform"), "y", 0.0)
	oRot := getFloat(oc, "rotation", 0.0)
	oFh := getBool(getMapDef(oc, "flip"), "horizontal", false)
	oFv := getBool(getMapDef(oc, "flip"), "vertical", false)
	oSpd := getFloat(compoundSeg, "speed", 1.0)

	var result []InnerSeg

	for _, t := range getSlice(inner, "tracks") {
		track, ok := t.(map[string]interface{})
		if !ok || getString(track, "type", "") != "video" {
			continue
		}

		segments := getSlice(track, "segments")
		sort.Slice(segments, func(i, j int) bool {
			si, _ := segments[i].(map[string]interface{})
			sj, _ := segments[j].(map[string]interface{})
			return getInt64(getMapDef(si, "target_timerange"), "start", 0) <
				getInt64(getMapDef(sj, "target_timerange"), "start", 0)
		})

		for _, s := range segments {
			seg, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			ttr := getMapDef(seg, "target_timerange")
			tStart := getInt64(ttr, "start", 0)
			tEnd := tStart + getInt64(ttr, "duration", 0)

			if tEnd <= outerStart || tStart >= outerEnd {
				continue
			}

			innerMat, ok := innerMats[getString(seg, "material_id", "")]
			if !ok {
				continue
			}

			srcTr := getMapDef(seg, "source_timerange")
			srcStart := getInt64(srcTr, "start", 0)
			srcDur := getInt64(srcTr, "duration", 0)

			overlapStart := outerStart
			if tStart > overlapStart {
				overlapStart = tStart
			}
			overlapEnd := outerEnd
			if tEnd < overlapEnd {
				overlapEnd = tEnd
			}

			if overlapStart > tStart {
				trim := overlapStart - tStart
				srcStart += trim
				srcDur -= trim
			}
			if overlapEnd < tEnd {
				srcDur -= (tEnd - overlapEnd)
			}
			if srcDur <= 0 {
				continue
			}

			ic := getMapDef(seg, "clip")
			iSx := getFloat(getMapDef(ic, "scale"), "x", 1.0)
			iSy := getFloat(getMapDef(ic, "scale"), "y", 1.0)
			iTx := getFloat(getMapDef(ic, "transform"), "x", 0.0)
			iTy := getFloat(getMapDef(ic, "transform"), "y", 0.0)
			iRot := getFloat(ic, "rotation", 0.0)
			iFh := getBool(getMapDef(ic, "flip"), "horizontal", false)
			iFv := getBool(getMapDef(ic, "flip"), "vertical", false)
			iSpd := getFloat(seg, "speed", 1.0)

			composedSeg := map[string]interface{}{
				"source_timerange": map[string]interface{}{
					"start":    float64(srcStart),
					"duration": float64(srcDur),
				},
				"speed": iSpd * oSpd,
				"clip": map[string]interface{}{
					"scale":     map[string]interface{}{"x": iSx * oSx, "y": iSy * oSy},
					"transform": map[string]interface{}{"x": iTx*oSx + oTx, "y": iTy*oSy + oTy},
					"rotation":  iRot + oRot,
					"flip":      map[string]interface{}{"horizontal": iFh != oFh, "vertical": iFv != oFv},
				},
			}

			matPath := getString(innerMat, "path", "")
			if matPath != "" {
				result = append(result, InnerSeg{Seg: composedSeg, Mat: innerMat})
			} else if getString(innerMat, "material_name", "") != "" {
				nested := resolveCompoundSegments(composedSeg, innerMat, draftDir, depth+1)
				result = append(result, nested...)
			}
		}
		break // first video track only
	}

	return result
}

// ---------------------------------------------------------------------------
// Adjacent-segment merger
// ---------------------------------------------------------------------------

type transformKey struct {
	group  string
	sub    string
	field  string
	defVal interface{}
}

var transformKeys = []transformKey{
	{"clip", "scale", "x", 1.0},
	{"clip", "scale", "y", 1.0},
	{"clip", "transform", "x", 0.0},
	{"clip", "transform", "y", 0.0},
	{"clip", "rotation", "", 0.0},
	{"clip", "flip", "horizontal", false},
	{"clip", "flip", "vertical", false},
}

func transformsEqual(segA, segB map[string]interface{}) bool {
	for _, tk := range transformKeys {
		groupA := getMapDef(segA, tk.group)
		groupB := getMapDef(segB, tk.group)
		if tk.field == "" {
			switch def := tk.defVal.(type) {
			case float64:
				if math.Abs(getFloat(groupA, tk.sub, def)-getFloat(groupB, tk.sub, def)) > 1e-6 {
					return false
				}
			}
		} else {
			subA := getMapDef(groupA, tk.sub)
			subB := getMapDef(groupB, tk.sub)
			switch def := tk.defVal.(type) {
			case float64:
				if math.Abs(getFloat(subA, tk.field, def)-getFloat(subB, tk.field, def)) > 1e-6 {
					return false
				}
			case bool:
				if getBool(subA, tk.field, def) != getBool(subB, tk.field, def) {
					return false
				}
			}
		}
	}
	if math.Abs(getFloat(segA, "speed", 1.0)-getFloat(segB, "speed", 1.0)) > 1e-6 {
		return false
	}
	return true
}

func copySeg(seg map[string]interface{}) map[string]interface{} {
	s := make(map[string]interface{})
	for k, v := range seg {
		s[k] = v
	}
	srcTr := getMapDef(seg, "source_timerange")
	newSrcTr := make(map[string]interface{})
	for k, v := range srcTr {
		newSrcTr[k] = v
	}
	s["source_timerange"] = newSrcTr
	return s
}

func mergeAdjacentInnerSegs(innerSegs []InnerSeg, gapTolUs int64) []InnerSeg {
	if len(innerSegs) <= 1 {
		return innerSegs
	}

	var merged []InnerSeg
	curSeg := copySeg(innerSegs[0].Seg)
	curMat := innerSegs[0].Mat

	for _, is := range innerSegs[1:] {
		curSrcTr := curSeg["source_timerange"].(map[string]interface{})
		curEnd := getInt64(curSrcTr, "start", 0) + getInt64(curSrcTr, "duration", 0)
		nextStart := getInt64(getMapDef(is.Seg, "source_timerange"), "start", 0)

		if getString(is.Mat, "path", "") == getString(curMat, "path", "") &&
			abs64(nextStart-curEnd) <= gapTolUs &&
			transformsEqual(curSeg, is.Seg) {
			gap := nextStart - curEnd
			curDur := getInt64(curSrcTr, "duration", 0)
			nextDur := getInt64(getMapDef(is.Seg, "source_timerange"), "duration", 0)
			curSrcTr["duration"] = float64(curDur + gap + nextDur)
		} else {
			merged = append(merged, InnerSeg{Seg: curSeg, Mat: curMat})
			curSeg = copySeg(is.Seg)
			curMat = is.Mat
		}
	}
	merged = append(merged, InnerSeg{Seg: curSeg, Mat: curMat})
	return merged
}

// ---------------------------------------------------------------------------
// FFmpeg export — single segment
// ---------------------------------------------------------------------------

func exportSegment(
	segment, material map[string]interface{},
	outputPath string,
	canvasW, canvasH, outW, outH, fps int,
	useGPU, dryRun bool,
	onProgress func(pct float64, encoder string),
) (bool, string) {
	srcPath := getString(material, "path", "")
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		return false, fmt.Sprintf("[!] Source not found: %s", srcPath)
	}

	srcTr := getMapDef(segment, "source_timerange")
	srcStartS := getFloat(srcTr, "start", 0) / 1_000_000
	srcDurS := getFloat(srcTr, "duration", 0) / 1_000_000
	if srcDurS <= 0 {
		return false, "[!] Zero duration, skipping"
	}

	probe := probeVideo(srcPath)
	vf := buildVF(segment, probe.Width, probe.Height, canvasW, canvasH, outW, outH)

	cmd := buildFFmpegCmd(srcPath, outputPath, vf, srcStartS, srcDurS, fps, outW, outH, useGPU)

	if dryRun {
		return true, fmt.Sprintf("cmd: %s", strings.Join(cmd, " "))
	}

	_ = os.MkdirAll(filepath.Dir(outputPath), 0755)

	proc := exec.Command(cmd[0], cmd[1:]...)
	proc.SysProcAttr = noWindow
	var rawProg func(float64)
	if onProgress != nil {
		rawProg = func(p float64) { onProgress(p, "GPU") }
	}
	outStr, err := runFFmpegProgress(cmd, srcDurS, rawProg)
	if err != nil {
		if useGPU {
			cmdCPU := buildCPUFallback(cmd)
			var rawProg2 func(float64)
			if onProgress != nil {
				rawProg2 = func(p float64) { onProgress(p, "CPU") }
			}
			outStr2, err2 := runFFmpegProgress(cmdCPU, srcDurS, rawProg2)
			if err2 != nil {
				errMsg := tailStr(outStr2, 2000)
				return false, fmt.Sprintf("[x] FFmpeg error (CPU fallback): %s", errMsg)
			}
			return true, ""
		}
		errMsg := tailStr(outStr, 2000)
		return false, fmt.Sprintf("[x] FFmpeg error: %s", errMsg)
	}
	return true, ""
}

// ---------------------------------------------------------------------------
// FFmpeg export — compound (filter_complex concat)
// ---------------------------------------------------------------------------

func exportCompoundSegment(
	innerSegs []InnerSeg,
	outputPath string,
	canvasW, canvasH, outW, outH, fps int,
	useGPU, dryRun bool,
	onProgress func(pct float64, encoder string),
) (bool, string) {
	if len(innerSegs) == 0 {
		return false, "[!] No inner segments"
	}
	if len(innerSegs) == 1 {
		return exportSegment(innerSegs[0].Seg, innerSegs[0].Mat, outputPath,
			canvasW, canvasH, outW, outH, fps, useGPU, dryRun, onProgress)
	}

	// Collect unique source files
	srcIndex := make(map[string]int)
	var srcList []string
	for _, is := range innerSegs {
		p := getString(is.Mat, "path", "")
		if _, exists := srcIndex[p]; !exists {
			srcIndex[p] = len(srcList)
			srcList = append(srcList, p)
		}
	}

	cmd := []string{"ffmpeg", "-y"}
	for _, path := range srcList {
		if useGPU {
			cmd = append(cmd, "-hwaccel", "cuda")
		}
		cmd = append(cmd, "-i", path)
	}

	var filterParts []string
	var segLabels []string

	for i, is := range innerSegs {
		si := srcIndex[getString(is.Mat, "path", "")]
		srcStartS := getFloat(getMapDef(is.Seg, "source_timerange"), "start", 0) / 1_000_000
		srcDurS := getFloat(getMapDef(is.Seg, "source_timerange"), "duration", 0) / 1_000_000

		probe := probeVideo(getString(is.Mat, "path", ""))
		vf := buildVF(is.Seg, probe.Width, probe.Height, canvasW, canvasH, outW, outH)

		trimPart := fmt.Sprintf("trim=start=%.6f:duration=%.6f,setpts=PTS-STARTPTS", srcStartS, srcDurS)
		fullVF := fmt.Sprintf("%s,%s", trimPart, vf)
		label := fmt.Sprintf("vc%d", i)
		filterParts = append(filterParts, fmt.Sprintf("[%d:v]%s[%s]", si, fullVF, label))
		segLabels = append(segLabels, fmt.Sprintf("[%s]", label))
	}

	n := len(innerSegs)
	filterParts = append(filterParts,
		fmt.Sprintf("%sconcat=n=%d:v=1:a=0[vout]", strings.Join(segLabels, ""), n))

	cmd = append(cmd, "-filter_complex", strings.Join(filterParts, ";"))
	cmd = append(cmd, "-map", "[vout]")
	cmd = append(cmd, "-r", fmt.Sprintf("%d", fps), "-an")
	cmd = append(cmd, "-pix_fmt", "yuv420p")

	if useGPU {
		cmd = append(cmd, "-c:v", "h264_nvenc", "-preset", "p4", "-rc", "vbr",
			"-cq", "19", "-b:v", "0", "-maxrate", "80M", "-bufsize", "160M",
			"-profile:v", "high", "-level", "5.2")
	} else {
		cmd = append(cmd, "-c:v", "libx264", "-preset", "slow", "-crf", "18",
			"-profile:v", "high", "-level", "5.2")
	}

	cmd = append(cmd, "-colorspace", "bt709", "-color_primaries", "bt709",
		"-color_trc", "bt709", "-color_range", "tv")
	cmd = append(cmd, "-movflags", "+faststart", outputPath)

	if dryRun {
		return true, fmt.Sprintf("cmd: %s", strings.Join(cmd, " "))
	}

	_ = os.MkdirAll(filepath.Dir(outputPath), 0755)

	proc := exec.Command(cmd[0], cmd[1:]...)
	proc.SysProcAttr = noWindow
	// Compute total output duration for progress tracking
	var totalDurS float64
	for _, is := range innerSegs {
		totalDurS += getFloat(getMapDef(is.Seg, "source_timerange"), "duration", 0) / 1_000_000
	}
	var rawProg func(float64)
	if onProgress != nil {
		rawProg = func(p float64) { onProgress(p, "GPU") }
	}
	outStr, err := runFFmpegProgress(cmd, totalDurS, rawProg)
	if err != nil {
		if useGPU {
			cmdCPU := buildCPUFallback(cmd)
			var rawProg2 func(float64)
			if onProgress != nil {
				rawProg2 = func(p float64) { onProgress(p, "CPU") }
			}
			outStr2, err2 := runFFmpegProgress(cmdCPU, totalDurS, rawProg2)
			if err2 != nil {
				return false, fmt.Sprintf("[x] FFmpeg error (CPU fallback): %s", tailStr(outStr2, 2000))
			}
			return true, ""
		}
		return false, fmt.Sprintf("[x] FFmpeg error: %s", tailStr(outStr, 2000))
	}
	return true, ""
}

// ---------------------------------------------------------------------------
// FFmpeg command helpers
// ---------------------------------------------------------------------------

func buildFFmpegCmd(srcPath, outputPath, vf string, startS, durS float64, fps, outW, outH int, useGPU bool) []string {
	cmd := []string{"ffmpeg", "-y"}
	cmd = append(cmd, "-ss", fmt.Sprintf("%.6f", startS), "-t", fmt.Sprintf("%.6f", durS))
	if useGPU {
		cmd = append(cmd, "-hwaccel", "cuda")
	}
	cmd = append(cmd, "-i", srcPath)
	cmd = append(cmd, "-vf", vf)
	cmd = append(cmd, "-an")
	cmd = append(cmd, "-r", fmt.Sprintf("%d", fps))
	cmd = append(cmd, "-pix_fmt", "yuv420p")

	if useGPU {
		cmd = append(cmd, "-c:v", "h264_nvenc",
			"-preset", "p4", "-rc", "vbr",
			"-cq", "19", "-b:v", "0",
			"-maxrate", "80M", "-bufsize", "160M",
			"-profile:v", "high", "-level", "5.2")
	} else {
		cmd = append(cmd, "-c:v", "libx264",
			"-preset", "slow", "-crf", "18",
			"-profile:v", "high", "-level", "5.2")
	}

	cmd = append(cmd,
		"-colorspace", "bt709", "-color_primaries", "bt709",
		"-color_trc", "bt709", "-color_range", "tv")
	cmd = append(cmd, "-movflags", "+faststart", outputPath)
	return cmd
}

func buildCPUFallback(cmd []string) []string {
	var result []string
	skip := false
	for _, c := range cmd {
		if skip {
			skip = false
			continue
		}
		switch c {
		case "h264_nvenc":
			result = append(result, "libx264")
		case "-hwaccel":
			skip = true
		case "-rc":
			result = append(result, "-crf", "18")
			skip = true
		case "-cq", "-b:v", "-maxrate", "-bufsize":
			skip = true
		case "p4":
			result = append(result, "slow")
		default:
			result = append(result, c)
		}
	}
	return result
}

func tailStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[len(s)-maxLen:]
}

// splitCRLF splits on \r, \n, or \r\n — needed because FFmpeg uses \r for progress lines.
func splitCRLF(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i := 0; i < len(data); i++ {
		if data[i] == '\r' || data[i] == '\n' {
			token = data[:i]
			advance = i + 1
			if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
				advance = i + 2
			}
			return advance, token, nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// runFFmpegProgress runs an ffmpeg command, streaming stderr to parse time= progress.
// Returns combined output string (for error messages) and any error.
func runFFmpegProgress(cmd []string, durS float64, onProgress func(float64)) (string, error) {
	proc := exec.Command(cmd[0], cmd[1:]...)
	proc.SysProcAttr = noWindow

	pr, pw := io.Pipe()
	proc.Stderr = pw
	proc.Stdout = pw

	if err := proc.Start(); err != nil {
		pw.Close()
		return "", err
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- proc.Wait()
		pw.Close()
	}()

	timeRe := regexp.MustCompile(`time=(\d{2}):(\d{2}):(\d{2}\.\d+)`)
	sc := bufio.NewScanner(pr)
	sc.Split(splitCRLF)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)

	var lines []string
	for sc.Scan() {
		line := sc.Text()
		lines = append(lines, line)
		if onProgress != nil && durS > 0 {
			if m := timeRe.FindStringSubmatch(line); m != nil {
				h, _ := strconv.Atoi(m[1])
				min, _ := strconv.Atoi(m[2])
				sec, _ := strconv.ParseFloat(m[3], 64)
				elapsed := float64(h*3600+min*60) + sec
				pct := elapsed / durS
				if pct > 1.0 {
					pct = 1.0
				}
				onProgress(pct)
			}
		}
	}

	err := <-waitCh
	return strings.Join(lines, "\n"), err
}

// ---------------------------------------------------------------------------
// Export options & main orchestrator
// ---------------------------------------------------------------------------

type ExportOptions struct {
	ProjectDir   string `json:"project_dir"`
	OutputDir    string `json:"output_dir"`
	Track        string `json:"track"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FPS          int    `json:"fps"`
	Workers      int    `json:"workers"`
	UseGPU       bool   `json:"use_gpu"`
	DryRun       bool   `json:"dry_run"`
	Log          func(string)                                                         `json:"-"`
	// WorkerUpdate reports per-worker state: id (1-based), clip label (folder/file),
	// jobNum (e.g. "3/12"), progress [0,1], encoder ("GPU"/"CPU"), idle flag.
	WorkerUpdate func(id int, label, jobNum string, pct float64, encoder string, idle bool) `json:"-"`
	// OnJobDone is called after each clip finishes with cumulative done count and total.
	OnJobDone    func(done, total int)                                                `json:"-"`
}

func (o *ExportOptions) log(format string, args ...interface{}) {
	if o.Log != nil {
		o.Log(fmt.Sprintf(format, args...))
	}
}

type Job struct {
	Label      string
	OutputPath string
	Kind       string // "simple" | "compound"
	Segment    map[string]interface{}
	Material   map[string]interface{}
	InnerSegs  []InnerSeg
	Index      int // 1-based global job index (set after all jobs are planned)
	Total      int // total number of jobs (set after all jobs are planned)
}

func RunExport(opts ExportOptions) {
	// Clear probe cache each run
	probeCacheMu.Lock()
	probeCache = make(map[string]ProbeResult)
	probeCacheMu.Unlock()

	draftPath := filepath.Join(opts.ProjectDir, "draft_content.json")
	if _, err := os.Stat(draftPath); os.IsNotExist(err) {
		opts.log("[ERROR] draft_content.json not found in %s", opts.ProjectDir)
		return
	}

	data, err := os.ReadFile(draftPath)
	if err != nil {
		opts.log("[ERROR] Reading draft: %v", err)
		return
	}

	var draft map[string]interface{}
	if err := json.Unmarshal(data, &draft); err != nil {
		opts.log("[ERROR] Parsing draft JSON: %v", err)
		return
	}

	canvasCfg := getMapDef(draft, "canvas_config")
	canvasW := getInt(canvasCfg, "width", 1920)
	canvasH := getInt(canvasCfg, "height", 1080)
	outW := opts.Width
	outH := opts.Height
	if outW <= 0 {
		outW = 3840
	}
	if outH <= 0 {
		outH = 2160
	}
	fps := opts.FPS
	if fps <= 0 {
		fps = 30
	}

	// Build materials lookup
	materials := make(map[string]map[string]interface{})
	matSection := getMap(draft, "materials")
	if matSection != nil {
		for _, v := range getSlice(matSection, "videos") {
			if m, ok := v.(map[string]interface{}); ok {
				id := getString(m, "id", "")
				if id != "" {
					materials[id] = m
				}
			}
		}
	}

	opts.log("Project : %s", opts.ProjectDir)
	opts.log("Output  : %s", opts.OutputDir)
	opts.log("Canvas: %dx%d  →  Output: %dx%d @ %d fps", canvasW, canvasH, outW, outH, fps)
	opts.log("Materials loaded: %d", len(materials))

	// GPU detection
	useGPU := opts.UseGPU
	if useGPU {
		useGPU = hasNvidiaGPU()
		if useGPU {
			opts.log("Encoder: h264_nvenc (GPU)")
		} else {
			opts.log("Encoder: libx264 (CPU, no GPU detected)")
		}
	} else {
		opts.log("Encoder: libx264 (CPU)")
	}

	if !opts.DryRun {
		_ = os.MkdirAll(opts.OutputDir, 0755)
	}

	// Filter video tracks
	var tracks []map[string]interface{}
	for _, t := range getSlice(draft, "tracks") {
		track, ok := t.(map[string]interface{})
		if !ok || getString(track, "type", "") != "video" {
			continue
		}
		if opts.Track != "" {
			name := strings.ToLower(getString(track, "name", ""))
			if !strings.Contains(name, strings.ToLower(opts.Track)) {
				continue
			}
		}
		tracks = append(tracks, track)
	}

	// Plan jobs
	var jobs []Job
	usedFolderNames := make(map[string]bool)

	for trackIdx, track := range tracks {
		trackName := getString(track, "name", "")
		segments := getSlice(track, "segments")

		sort.Slice(segments, func(i, j int) bool {
			si, _ := segments[i].(map[string]interface{})
			sj, _ := segments[j].(map[string]interface{})
			return getInt64(getMapDef(si, "target_timerange"), "start", 0) <
				getInt64(getMapDef(sj, "target_timerange"), "start", 0)
		})

		if len(segments) == 0 {
			continue
		}

		// Determine folder name from first segment's source
		folderName := ""
		for _, s := range segments {
			seg, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			mat := materials[getString(seg, "material_id", "")]
			if mat == nil {
				continue
			}
			if p := getString(mat, "path", ""); p != "" {
				folderName = sanitizeName(fileStem(p))
				break
			}
			if getString(mat, "material_name", "") != "" {
				inner := resolveCompoundSegments(seg, mat, opts.ProjectDir, 0)
				if len(inner) > 0 {
					if fp := getString(inner[0].Mat, "path", ""); fp != "" {
						folderName = sanitizeName(fileStem(fp))
						break
					}
				}
			}
		}
		if folderName == "" {
			folderName = sanitizeName(trackName)
			if folderName == "" || folderName == "track" {
				folderName = fmt.Sprintf("track_%d", trackIdx+1)
			}
		}

		baseName := folderName
		counter := 2
		for usedFolderNames[folderName] {
			folderName = fmt.Sprintf("%s_%d", baseName, counter)
			counter++
		}
		usedFolderNames[folderName] = true

		trackOutDir := filepath.Join(opts.OutputDir, folderName)
		if !opts.DryRun {
			_ = os.MkdirAll(trackOutDir, 0755)
		}

		total := len(segments)
		opts.log("\nTrack: '%s'  →  %s  (%d clips)", trackName, trackOutDir, total)

		for idx, s := range segments {
			seg, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			matID := getString(seg, "material_id", "")
			mat := materials[matID]
			outPath := filepath.Join(trackOutDir, fmt.Sprintf("%d.mp4", idx+1))
			label := fmt.Sprintf("  [%d/%d] %s", idx+1, total, outPath)

			if mat == nil {
				opts.log("%s", label)
				opts.log("    [!] No material, skipping")
				continue
			}

			if getString(mat, "path", "") == "" && getString(mat, "material_name", "") != "" {
				innerSegs := resolveCompoundSegments(seg, mat, opts.ProjectDir, 0)
				if len(innerSegs) == 0 {
					opts.log("%s", label)
					opts.log("    [!] Compound '%s' could not be resolved, skipping", getString(mat, "material_name", ""))
					continue
				}
				innerSegs = mergeAdjacentInnerSegs(innerSegs, 2000)
				if len(innerSegs) == 1 {
					jobs = append(jobs, Job{Label: label, OutputPath: outPath, Kind: "simple",
						Segment: innerSegs[0].Seg, Material: innerSegs[0].Mat})
				} else {
					jobs = append(jobs, Job{Label: label, OutputPath: outPath, Kind: "compound",
						InnerSegs: innerSegs})
				}
			} else {
				jobs = append(jobs, Job{Label: label, OutputPath: outPath, Kind: "simple",
					Segment: seg, Material: mat})
			}
		}
	}

	if len(jobs) == 0 {
		opts.log("\nNo jobs to export.")
		return
	}

	// Assign global indices now that total is known.
	total := len(jobs)
	for i := range jobs {
		jobs[i].Index = i + 1
		jobs[i].Total = total
	}
	if opts.OnJobDone != nil {
		opts.OnJobDone(0, total)
	}

	// Execute with channel-based worker pool so each goroutine has a stable ID (1..N).
	workers := opts.Workers
	if workers < 1 {
		workers = 1
	}
	opts.log("\nExporting %d clips with %d worker(s)...", total, workers)

	type workerSlot struct{ id int }
	workerCh := make(chan workerSlot, workers)
	for i := 1; i <= workers; i++ {
		workerCh <- workerSlot{id: i}
	}

	if opts.WorkerUpdate != nil {
		for i := 1; i <= workers; i++ {
			opts.WorkerUpdate(i, "", "", 0, "", true)
		}
	}

	var totalExported, totalSkipped, doneCount int32
	var wg sync.WaitGroup

	for _, job := range jobs {
		wg.Add(1)
		slot := <-workerCh
		go func(j Job, s workerSlot) {
			defer wg.Done()
			defer func() {
				if opts.WorkerUpdate != nil {
					opts.WorkerUpdate(s.id, "", "", 0, "", true)
				}
				workerCh <- s
			}()

			dir := filepath.Base(filepath.Dir(j.OutputPath))
			base := filepath.Base(j.OutputPath)
			clipLabel := dir + "/" + base
			jobNum := fmt.Sprintf("%d/%d", j.Index, j.Total)

			initialEncoder := "CPU"
			if useGPU {
				initialEncoder = "GPU"
			}
			if opts.WorkerUpdate != nil {
				opts.WorkerUpdate(s.id, clipLabel, jobNum, 0, initialEncoder, false)
			}
			onProgress := func(pct float64, encoder string) {
				if opts.WorkerUpdate != nil {
					opts.WorkerUpdate(s.id, clipLabel, jobNum, pct, encoder, false)
				}
			}

			opts.log("%s", j.Label)

			var ok bool
			var msg string

			if j.Kind == "compound" {
				opts.log("    compound: %d segments (filter_complex)", len(j.InnerSegs))
				ok, msg = exportCompoundSegment(j.InnerSegs, j.OutputPath,
					canvasW, canvasH, outW, outH, fps, useGPU, opts.DryRun, onProgress)
			} else {
				ok, msg = exportSegment(j.Segment, j.Material, j.OutputPath,
					canvasW, canvasH, outW, outH, fps, useGPU, opts.DryRun, onProgress)
			}

			if msg != "" {
				opts.log("    %s", msg)
			}

			if ok {
				if opts.WorkerUpdate != nil {
					opts.WorkerUpdate(s.id, clipLabel, jobNum, 1.0, initialEncoder, false)
				}
				atomic.AddInt32(&totalExported, 1)
			} else {
				atomic.AddInt32(&totalSkipped, 1)
			}
			done := int(atomic.AddInt32(&doneCount, 1))
			if opts.OnJobDone != nil {
				opts.OnJobDone(done, total)
			}
		}(job, slot)
	}

	wg.Wait()

	opts.log("\n✅ Done: %d clips exported, %d skipped → %s",
		atomic.LoadInt32(&totalExported), atomic.LoadInt32(&totalSkipped), opts.OutputDir)
}
