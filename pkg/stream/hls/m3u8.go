package hls

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Variant represents a stream variant in a master playlist.
type Variant struct {
	URL        string
	Bandwidth  int
	Resolution string
	FrameRate  float64
	Codecs     string
	Video      string // GROUP-ID reference
	Audio      string // GROUP-ID reference
	Name       string // NAME or IVS-NAME attribute
}

// Media represents a rendition in a master playlist.
type Media struct {
	Type    string // "AUDIO", "VIDEO"
	GroupID string
	Name    string
	Default bool
	URI     string
}

// MasterPlaylist holds parsed master playlist data.
type MasterPlaylist struct {
	Variants []Variant
	Media    []Media
}

// MediaPlaylist holds parsed media playlist data.
type MediaPlaylist struct {
	TargetDuration float64
	Segments       []Segment
	DateRanges     []DateRange
	MediaSequence  int
	Ended          bool
	Map            *MapEntry
}

// ParseMaster parses an HLS master playlist from the given data string,
// resolving relative URLs against baseURL.
func ParseMaster(data string, baseURL string) (*MasterPlaylist, error) {
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "#EXTM3U") {
		return nil, fmt.Errorf("m3u8: not a valid M3U8 playlist")
	}

	playlist := &MasterPlaylist{}
	var pendingVariant *Variant

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			a := parseAttrList(line[len("#EXT-X-STREAM-INF:"):])
			v := Variant{
				Bandwidth:  a.Int("BANDWIDTH"),
				Resolution: a.Get("RESOLUTION"),
				FrameRate:  a.Float("FRAME-RATE"),
				Codecs:     a.Get("CODECS"),
				Video:      a.Get("VIDEO"),
				Audio:      a.Get("AUDIO"),
				Name:       a.Get("NAME"),
			}
			if v.Name == "" {
				v.Name = a.Get("IVS-NAME")
			}
			pendingVariant = &v
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-MEDIA:") {
			a := parseAttrList(line[len("#EXT-X-MEDIA:"):])
			m := Media{
				Type:    a.Get("TYPE"),
				GroupID: a.Get("GROUP-ID"),
				Name:    a.Get("NAME"),
				Default: strings.EqualFold(a.Get("DEFAULT"), "YES"),
				URI:     a.Get("URI"),
			}
			if m.URI != "" {
				m.URI = resolveURL(baseURL, m.URI)
			}
			playlist.Media = append(playlist.Media, m)
			continue
		}

		// Non-tag, non-comment line: treat as variant URL if we have a pending variant
		if pendingVariant != nil && !strings.HasPrefix(line, "#") {
			pendingVariant.URL = resolveURL(baseURL, line)

			// IVS-NAME backward compatibility: create synthetic audio media entries
			if pendingVariant.Audio != "" && pendingVariant.Name != "" {
				audioName := pendingVariant.Audio
				if audioName == "audio_only" || audioName == "audio" {
					found := false
					for _, m := range playlist.Media {
						if m.GroupID == audioName {
							found = true
							break
						}
					}
					if !found {
						playlist.Media = append(playlist.Media, Media{
							Type:    "AUDIO",
							GroupID: audioName,
							Name:    audioName,
							Default: true,
						})
					}
				}
			}

			playlist.Variants = append(playlist.Variants, *pendingVariant)
			pendingVariant = nil
			continue
		}

		if pendingVariant == nil {
			// Bare URL without stream info, skip
			continue
		}
	}

	return playlist, nil
}

// ParseMedia parses an HLS media playlist from the given data string,
// resolving relative URLs against baseURL.
func ParseMedia(data string, baseURL string) (*MediaPlaylist, error) {
	lines := strings.Split(strings.ReplaceAll(data, "\r\n", "\n"), "\n")
	if len(lines) == 0 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "#EXTM3U") {
		return nil, fmt.Errorf("m3u8: not a valid M3U8 playlist")
	}

	playlist := &MediaPlaylist{}
	var currentKey *Key
	var currentByteRange *ByteRange
	var currentDate time.Time
	var currentDuration float64
	var currentTitle string
	var hasDuration bool
	var discontinuity bool
	mediaSequence := 0
	segIndex := 0
	var regularDurationSum float64
	var regularDurationCount int

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "#EXT-X-TARGETDURATION:"):
			val := line[len("#EXT-X-TARGETDURATION:"):]
			var err error
			playlist.TargetDuration, err = strconv.ParseFloat(strings.TrimSpace(val), 64)
			if err != nil {
				slog.Warn("hls m3u8: invalid target duration", "value", val, "err", err)
			}

		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			val := line[len("#EXT-X-MEDIA-SEQUENCE:"):]
			var err error
			mediaSequence, err = strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				slog.Warn("hls m3u8: invalid media sequence", "value", val, "err", err)
			}
			playlist.MediaSequence = mediaSequence

		case strings.HasPrefix(line, "#EXTINF:"):
			val := line[len("#EXTINF:"):]
			// Remove trailing comma if present
			val = strings.TrimSuffix(val, ",")
			parts := strings.SplitN(val, ",", 2)
			var err error
			currentDuration, err = strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
			if err != nil {
				slog.Warn("hls m3u8: invalid segment duration", "value", parts[0], "err", err)
			}
			if len(parts) > 1 {
				currentTitle = strings.TrimSpace(parts[1])
			} else {
				currentTitle = ""
			}
			hasDuration = true

		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			a := parseAttrList(line[len("#EXT-X-KEY:"):])
			method := a.Get("METHOD")
			if strings.EqualFold(method, "NONE") {
				currentKey = nil
			} else {
				k := &Key{
					Method: method,
					URI:    resolveURL(baseURL, a.Get("URI")),
				}
				ivStr := a.Get("IV")
				if ivStr != "" {
					rawIV := ivStr
					ivStr = strings.TrimPrefix(ivStr, "0x")
					ivStr = strings.TrimPrefix(ivStr, "0X")
					var err error
					k.IV, err = hex.DecodeString(ivStr)
					if err != nil {
						slog.Warn("hls m3u8: invalid encryption IV", "value", rawIV, "err", err)
					}
				}
				currentKey = k
			}

		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			a := parseAttrList(line[len("#EXT-X-MAP:"):])
			m := &MapEntry{
				URI: resolveURL(baseURL, a.Get("URI")),
			}
			if brStr := a.Get("BYTERANGE"); brStr != "" {
				m.ByteRange = parseByteRange(brStr)
			}
			playlist.Map = m

		case strings.HasPrefix(line, "#EXT-X-BYTERANGE:"):
			val := line[len("#EXT-X-BYTERANGE:"):]
			currentByteRange = parseByteRange(strings.TrimSpace(val))

		case strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"):
			val := line[len("#EXT-X-PROGRAM-DATE-TIME:"):]
			t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(val))
			if err != nil {
				// Try alternate formats
				t, err = time.Parse("2006-01-02T15:04:05.000Z", strings.TrimSpace(val))
				if err != nil {
					t, _ = time.Parse(time.RFC3339, strings.TrimSpace(val))
				}
			}
			currentDate = t

		case strings.HasPrefix(line, "#EXT-X-DATERANGE:"):
			a := parseAttrList(line[len("#EXT-X-DATERANGE:"):])
			dr := DateRange{
				ID:    a.Get("ID"),
				Class: a.Get("CLASS"),
			}
			if startStr := a.Get("START-DATE"); startStr != "" {
				dr.Start, _ = time.Parse(time.RFC3339Nano, startStr)
			}
			if endStr := a.Get("END-DATE"); endStr != "" {
				dr.End, _ = time.Parse(time.RFC3339Nano, endStr)
			}
			dr.Duration = a.Float("DURATION")
			dr.PlannedDuration = a.Float("PLANNED-DURATION")
			dr.EndOnNext = strings.EqualFold(a.Get("END-ON-NEXT"), "YES")
			dr.X = a.XAttrs()

			playlist.DateRanges = append(playlist.DateRanges, dr)

		case line == "#EXT-X-DISCONTINUITY":
			discontinuity = true

		case line == "#EXT-X-ENDLIST":
			playlist.Ended = true

		case strings.HasPrefix(line, "#EXT-X-TWITCH-PREFETCH:"):
			prefetchURL := resolveURL(baseURL, strings.TrimSpace(line[len("#EXT-X-TWITCH-PREFETCH:"):]))
			var avgDuration float64
			if regularDurationCount > 0 {
				avgDuration = regularDurationSum / float64(regularDurationCount)
			}
			seg := Segment{
				Num:           mediaSequence + segIndex,
				URL:           prefetchURL,
				Duration:      avgDuration,
				Discontinuity: discontinuity,
				Map:           playlist.Map,
				Prefetch:      true,
			}
			if currentKey != nil {
				keyCopy := *currentKey
				if keyCopy.IV == nil {
					keyCopy.IV = defaultIV(mediaSequence + segIndex)
				}
				seg.Key = &keyCopy
			}
			if !currentDate.IsZero() {
				seg.Date = currentDate
				currentDate = time.Time{}
			}
			discontinuity = false
			playlist.Segments = append(playlist.Segments, seg)
			segIndex++

		case !strings.HasPrefix(line, "#"):
			if !hasDuration {
				continue
			}
			seg := Segment{
				Num:           mediaSequence + segIndex,
				URL:           resolveURL(baseURL, line),
				Duration:      currentDuration,
				Title:         currentTitle,
				Discontinuity: discontinuity,
				Map:           playlist.Map,
			}
			if currentKey != nil {
				keyCopy := *currentKey
				if keyCopy.IV == nil {
					keyCopy.IV = defaultIV(mediaSequence + segIndex)
				}
				seg.Key = &keyCopy
			}
			if currentByteRange != nil {
				seg.ByteRange = currentByteRange
				currentByteRange = nil
			}
			if !currentDate.IsZero() {
				seg.Date = currentDate
				currentDate = time.Time{}
			}
			discontinuity = false
			hasDuration = false
			regularDurationSum += currentDuration
			regularDurationCount++
			playlist.Segments = append(playlist.Segments, seg)
			segIndex++
		}
	}

	return playlist, nil
}

// defaultIV returns the HLS default AES-128 IV derived from a segment
// sequence number: 16 bytes big-endian, padded with leading zeros.
func defaultIV(seqNum int) []byte {
	iv := make([]byte, 16)
	iv[12] = byte(seqNum >> 24)
	iv[13] = byte(seqNum >> 16)
	iv[14] = byte(seqNum >> 8)
	iv[15] = byte(seqNum)
	return iv
}

// resolveURL resolves a possibly relative URL against a base URL.
func resolveURL(base, ref string) string {
	if ref == "" {
		return ""
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if refURL.IsAbs() {
		return ref
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return ref
	}
	return baseURL.ResolveReference(refURL).String()
}

// parseByteRange parses a byte range string like "1024@0" or "1024".
func parseByteRange(s string) *ByteRange {
	parts := strings.SplitN(s, "@", 2)
	length, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return nil
	}
	br := &ByteRange{Length: length}
	if len(parts) > 1 {
		var err error
		br.Offset, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			slog.Warn("hls m3u8: invalid byte range offset", "value", parts[1], "err", err)
		}
	}
	return br
}

// attrList is a parsed HLS attribute list. Values have their enclosing
// quotes stripped; callers treat all accessors uniformly regardless of
// whether the original grammar was quoted or unquoted.
type attrList map[string]string

// parseAttrList splits an HLS attribute list (the portion after `#TAG:`)
// into a map of attribute name → value. Each attribute is scanned once;
// this replaces repeated per-accessor substring searches that previously
// scaled O(attrs × accessors) per tag.
func parseAttrList(s string) attrList {
	out := make(attrList)
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == ',') {
			i++
		}
		keyStart := i
		for i < len(s) && s[i] != '=' {
			i++
		}
		if i >= len(s) || i == keyStart {
			break
		}
		key := s[keyStart:i]
		i++ // consume '='
		if i >= len(s) {
			out[key] = ""
			break
		}
		if s[i] == '"' {
			i++
			vStart := i
			for i < len(s) && s[i] != '"' {
				i++
			}
			out[key] = s[vStart:i]
			if i < len(s) {
				i++ // consume closing quote
			}
		} else {
			vStart := i
			for i < len(s) && s[i] != ',' {
				i++
			}
			out[key] = strings.TrimSpace(s[vStart:i])
		}
	}
	return out
}

// Get returns the value for name, or "" if absent.
func (a attrList) Get(name string) string { return a[name] }

// Int returns the value parsed as an integer, or 0 on absence or parse error.
func (a attrList) Int(name string) int {
	s, ok := a[name]
	if !ok || s == "" {
		return 0
	}
	v, _ := strconv.Atoi(s)
	return v
}

// Float returns the value parsed as a float64, or 0 on absence or parse error.
func (a attrList) Float(name string) float64 {
	s, ok := a[name]
	if !ok || s == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// XAttrs returns all X-prefixed attributes (custom client namespace).
func (a attrList) XAttrs() map[string]string {
	out := map[string]string{}
	for k, v := range a {
		if strings.HasPrefix(k, "X-") {
			out[k] = v
		}
	}
	return out
}
