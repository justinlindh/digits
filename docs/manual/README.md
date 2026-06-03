# Device manual

The printed "Getting Started" guide that ships with each Digits phone. It is a
10-page A4 booklet in the dial-up theme, designed to print on a home laserjet
(paper-white interiors, color reserved for title bars and the two covers).

## Files

- `getting-started.html` source booklet (10 A4 pages). Open in a browser and use
  the "Print this guide" button, or print to PDF.
- `getting-started.pdf` rendered output, ready to print or mail.
- `static/` stylesheets (`dialup.css`, `manual.css`) and the phone image.
- `reference-screenshots/` real captures used to keep the content accurate:
  the web-app pairing page, the per-line answering-machine panel, and the
  device's Wi-Fi captive portal. Reference only, not embedded in the booklet.

## Regenerating the PDF

The booklet's print CSS sets A4 page size, one page per sheet, and forces the
title-bar backgrounds through (`print-color-adjust: exact`). Render with headless
Chrome:

```
cd docs/manual
PROF=$(mktemp -d)
google-chrome-stable --headless=new --disable-gpu --no-pdf-header-footer \
  --run-all-compositor-stages-before-draw --virtual-time-budget=6000 \
  --user-data-dir="$PROF" \
  --print-to-pdf="getting-started.pdf" "file://$PWD/getting-started.html"
rm -rf "$PROF"
```

## Content source of truth

Every factual claim (service codes, voicemail flow, call return, party line,
LED patterns, the setup and pairing flow) is verified against the firmware,
`pi/digitsd/`, and `server/` source. When device behavior changes, update the
booklet to match. The closest in-repo references are `firmware/src/phone_fsm.c`,
`pi/digitsd/internal/phone/servicecodes.go`, `pi/digitsd/cmd/digitsd/voicemail.go`,
`pi/digitsd/cmd/digitsd/setup.go`, and `server/internal/pairing/pairing.go`.
