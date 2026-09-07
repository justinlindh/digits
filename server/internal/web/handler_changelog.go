package web

import (
	"log/slog"
	"net/http"

	"github.com/justinlindh/digits/server/internal/line"
	"github.com/justinlindh/digits/server/internal/updates"
)

type changelogRelease struct {
	Version    string
	Notes      string
	Date       string
	AudioURL   string
	PhoneCount int
	TotalCount int
}

type changelogData struct {
	Server   []changelogRelease
	Software []changelogRelease
	Firmware []changelogRelease
}

func (h *Handler) handleChangelog(w http.ResponseWriter, r *http.Request) {
	hh := h.activeHousehold(r)
	var lines []line.Line
	if hh != nil && h.lineStore != nil {
		ll, err := h.lineStore.ListByHousehold(r.Context(), hh.ID)
		if err == nil {
			lines = ll
		} else {
			slog.ErrorContext(r.Context(), "changelog: list lines failed", "household_id", hh.ID, "err", err)
		}
	}

	var idx *updates.ReleaseIndex
	if h.releases != nil {
		idx = h.releases.ReleaseIndex()
	}

	var data changelogData
	if idx != nil {
		data.Server = h.buildChangelogSection(idx, updates.ComponentServer, nil)
		data.Software = h.buildChangelogSection(idx, updates.ComponentPi, lines)
		data.Firmware = h.buildChangelogSection(idx, updates.ComponentFirmware, lines)
	}

	renderWith(r.Context(), w, h.tmplChangelog, "changelog-content", data)
}

func (h *Handler) buildChangelogSection(idx *updates.ReleaseIndex, component string, lines []line.Line) []changelogRelease {
	releases := idx.SortedReleases(component)
	out := make([]changelogRelease, 0, len(releases))

	var totalDevices int
	versionCounts := make(map[string]int)
	for _, line := range lines {
		infos := h.hub.AllDeviceInfoForLine(line.Number, line.ID)
		for _, info := range infos {
			var ver string
			switch component {
			case updates.ComponentPi:
				ver = info.PiVersion
			case updates.ComponentFirmware:
				ver = info.FirmwareVersion
			default:
				continue
			}
			if ver != "" {
				versionCounts[ver]++
				totalDevices++
			}
		}
	}

	for _, r := range releases {
		var audioURL string
		if r.AudioURL != "" {
			audioURL = "/api/release-audio/" + component + "/" + r.Version
		}
		out = append(out, changelogRelease{
			Version:    r.Version,
			Notes:      r.Notes,
			Date:       r.Date,
			AudioURL:   audioURL,
			PhoneCount: versionCounts[r.Version],
			TotalCount: totalDevices,
		})
	}
	return out
}
