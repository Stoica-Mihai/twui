package hls

import (
	"encoding/hex"
	"fmt"
	"log"
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
			attrs := line[len("#EXT-X-STREAM-INF:"):]
			v := Variant{}
			v.Bandwidth = attrInt(attrs, "BANDWIDTH")
			v.Resolution = attrString(attrs, "RESOLUTION")
			v.FrameRate = attrFloat(attrs, "FRAME-RATE")
			v.Codecs = attrQuoted(attrs, "CODECS")
			v.Video = attrQuoted(attrs, "VIDEO")
			v.Audio = attrQuoted(attrs, "AUDIO")
			v.Name = attrQuoted(attrs, "NAME")
			if v.Name == "" {
				v.Name = attrQuoted(attrs, "IVS-NAME")
			}
			pendingVariant = &v
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-MEDIA:") {
			attrs := line[len("#EXT-X-MEDIA:"):]
			m := Media{
				Type:    attrQuoted(attrs, "TYPE"),
				GroupID: attrQuoted(attrs, "GROUP-ID"),
				Name:    attrQuoted(attrs, "NAME"),
				Default: strings.EqualFold(attrString(attrs, "DEFAULT"), "YES"),
				URI:     attrQuoted(attrs, "URI"),
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
				log.Printf("hls: m3u8: invalid target duration %q: %v", val, err)
			}

		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			val := line[len("#EXT-X-MEDIA-SEQUENCE:"):]
			var err error
			mediaSequence, err = strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				log.Printf("hls: m3u8: invalid media sequence %q: %v", val, err)
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
				log.Printf("hls: m3u8: invalid segment duration %q: %v", parts[0], err)
			}
			if len(parts) > 1 {
				currentTitle = strings.TrimSpace(parts[1])
			} else {
				currentTitle = ""
			}
			hasDuration = true

		case strings.HasPrefix(line, "#EXT-X-KEY:"):
			attrs := line[len("#EXT-X-KEY:"):]
			method := attrString(attrs, "METHOD")
			if strings.EqualFold(method, "NONE") {
				currentKey = nil
			} else {
				k := &Key{
					Method: method,
					URI:    resolveURL(baseURL, attrQuoted(attrs, "URI")),
				}
				ivStr := attrString(attrs, "IV")
				if ivStr != "" {
					rawIV := ivStr
					ivStr = strings.TrimPrefix(ivStr, "0x")
					ivStr = strings.TrimPrefix(ivStr, "0X")
					var err error
					k.IV, err = hex.DecodeString(ivStr)
					if err != nil {
						log.Printf("hls: m3u8: invalid encryption IV %q: %v", rawIV, err)
					}
				}
				currentKey = k
			}

		case strings.HasPrefix(line, "#EXT-X-MAP:"):
			attrs := line[len("#EXT-X-MAP:"):]
			m := &MapEntry{
				URI: resolveURL(baseURL, attrQuoted(attrs, "URI")),
			}
			brStr := attrQuoted(attrs, "BYTERANGE")
			if brStr != "" {
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
			attrs := line[len("#EXT-X-DATERANGE:"):]
			dr := DateRange{
				ID:    attrQuoted(attrs, "ID"),
				Class: attrQuoted(attrs, "CLASS"),
			}
			startStr := attrQuoted(attrs, "START-DATE")
			if startStr != "" {
				dr.Start, _ = time.Parse(time.RFC3339Nano, startStr)
			}
			endStr := attrQuoted(attrs, "END-DATE")
			if endStr != "" {
				dr.End, _ = time.Parse(time.RFC3339Nano, endStr)
			}
			durStr := attrString(attrs, "DURATION")
			if durStr != "" {
				dr.Duration, _ = strconv.ParseFloat(durStr, 64)
			}
			plannedStr := attrString(attrs, "PLANNED-DURATION")
			if plannedStr != "" {
				dr.PlannedDuration, _ = strconv.ParseFloat(plannedStr, 64)
			}
			dr.EndOnNext = strings.EqualFold(attrString(attrs, "END-ON-NEXT"), "YES")

			// Parse X-prefixed attributes
			dr.X = parseXAttributes(attrs)

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
					iv := make([]byte, 16)
					seqNum := mediaSequence + segIndex
					iv[12] = byte(seqNum >> 24)
					iv[13] = byte(seqNum >> 16)
					iv[14] = byte(seqNum >> 8)
					iv[15] = byte(seqNum)
					keyCopy.IV = iv
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
					// Default IV is the segment sequence number as a 16-byte big-endian value
					iv := make([]byte, 16)
					seqNum := mediaSequence + segIndex
					iv[12] = byte(seqNum >> 24)
					iv[13] = byte(seqNum >> 16)
					iv[14] = byte(seqNum >> 8)
					iv[15] = byte(seqNum)
					keyCopy.IV = iv
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
			log.Printf("hls: m3u8: invalid byte range offset %q: %v", parts[1], err)
		}
	}
	return br
}

// attrString extracts an unquoted attribute value from an HLS attribute list.
func attrString(attrs, name string) string {
	key := name + "="
	idx := findAttr(attrs, key)
	if idx < 0 {
		return ""
	}
	rest := attrs[idx+len(key):]
	if len(rest) > 0 && rest[0] == '"' {
		// Quoted value — use attrQuoted instead
		return attrQuoted(attrs, name)
	}
	end := strings.IndexByte(rest, ',')
	if end < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:end])
}

// attrQuoted extracts a quoted attribute value from an HLS attribute list.
func attrQuoted(attrs, name string) string {
	key := name + "=\""
	idx := findAttr(attrs, key)
	if idx < 0 {
		return ""
	}
	rest := attrs[idx+len(key):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// attrInt extracts an integer attribute value from an HLS attribute list.
func attrInt(attrs, name string) int {
	s := attrString(attrs, name)
	if s == "" {
		return 0
	}
	v, _ := strconv.Atoi(s)
	return v
}

// attrFloat extracts a float attribute value from an HLS attribute list.
func attrFloat(attrs, name string) float64 {
	s := attrString(attrs, name)
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// findAttr finds the position of an attribute key in the attribute string,
// ensuring it is at the start or preceded by a comma/space.
func findAttr(attrs, key string) int {
	pos := 0
	for {
		idx := strings.Index(attrs[pos:], key)
		if idx < 0 {
			return -1
		}
		absIdx := pos + idx
		if absIdx == 0 {
			return absIdx
		}
		prev := attrs[absIdx-1]
		if prev == ',' || prev == ' ' {
			return absIdx
		}
		pos = absIdx + 1
	}
}

// parseXAttributes extracts all X-prefixed attributes from an attribute string.
func parseXAttributes(attrs string) map[string]string {
	result := map[string]string{}
	// Scan for X- prefixed attributes
	pos := 0
	for pos < len(attrs) {
		// Find next X- key
		idx := strings.Index(attrs[pos:], "X-")
		if idx < 0 {
			break
		}
		absIdx := pos + idx
		// Ensure it's at start or after comma/space
		if absIdx > 0 {
			prev := attrs[absIdx-1]
			if prev != ',' && prev != ' ' {
				pos = absIdx + 1
				continue
			}
		}
		// Find the = sign
		eqIdx := strings.IndexByte(attrs[absIdx:], '=')
		if eqIdx < 0 {
			break
		}
		key := attrs[absIdx : absIdx+eqIdx]
		valStart := absIdx + eqIdx + 1
		if valStart >= len(attrs) {
			break
		}
		var val string
		if attrs[valStart] == '"' {
			// Quoted value
			endQuote := strings.IndexByte(attrs[valStart+1:], '"')
			if endQuote < 0 {
				break
			}
			val = attrs[valStart+1 : valStart+1+endQuote]
			pos = valStart + 1 + endQuote + 1
		} else {
			// Unquoted value
			endComma := strings.IndexByte(attrs[valStart:], ',')
			if endComma < 0 {
				val = strings.TrimSpace(attrs[valStart:])
				pos = len(attrs)
			} else {
				val = strings.TrimSpace(attrs[valStart : valStart+endComma])
				pos = valStart + endComma + 1
			}
		}
		// Skip standard attributes that happen to start with uppercase
		// Only include truly custom X- attributes
		if strings.HasPrefix(key, "X-") {
			result[key] = val
		}
	}
	return result
}
