# Digits Per-Device Cost Estimate

All costs are estimates based on pricing observed in April 2026. Actual costs will vary based on supplier availability, shipping, and order quantity. Assumes a batch of 30 units.

---

## Per-Device Breakdown

| Item | Est. Cost | Source | Notes |
|------|-----------|--------|-------|
| Vintage desk phone | $33 | Various | Includes bell, keypad, handset, hookswitch mechanism |
| Raspberry Pi Zero 2 W | $15 | Authorized resellers | |
| Raspberry Pi Codec Zero | $20 | PiShop.us | DA7212 audio codec HAT -- hardest part to source |
| Assembled PCB (v2, JLCPCB) | ~$12 | JLCPCB | 2-layer, SMT assembled, includes RP2040 + all SMD components |
| THT connectors (hand-solder) | ~$4 | DigiKey / on hand | JST, barrel jack, screw terminal, pin headers, switch |
| 12V wall wart | ~$5 | Various | 2.1x5.5mm barrel, 1A+ |
| MicroSD card | ~$6 | Various | 8GB+ |
| Misc (stacking header, JST cable, standoffs) | ~$3 | Various | |
| **Total per device** | **~$98** | | |

---

## Batch Cost (30 units)

| Category | Est. Total |
|----------|-----------|
| Phones | $990 |
| Pi Zero 2 W (x30) | $450 |
| Codec Zero (x30) | $600 |
| Assembled PCBs (x30) | $250-350 |
| THT parts (x30) | $120 |
| 12V adapters (x30) | $150 |
| SD cards (x30) | $180 |
| Misc | $90 |
| **Batch total** | **~$2,830-2,930** |

---

## JLCPCB Assembly Cost Breakdown (30 boards)

| Fee | Est. Cost |
|-----|-----------|
| PCB fabrication (2-layer, 76x57mm, qty 30) | ~$40 |
| Setup fee | $8 |
| Stencil | $1.50 |
| Per-joint assembly (~150 joints x 30 boards) | ~$8 |
| Extended parts loading (RP2040, W25Q16, DRV8871) | ~$9 |
| Component costs (x30) | ~$100-120 |
| Standard assembly tier (required for QFN-56) | TBD |
| X-ray inspection (required for QFN) | TBD |
| **Subtotal** | **~$250-350** |

---

## Cost Reduction Opportunities

| Opportunity | Savings | Effort |
|-------------|---------|--------|
| Bare RP2040 vs Pico H (already in v2) | ~$3-4/unit | Done |
| Integrate DA7212 codec onto carrier board | ~$15-17/unit | Major schematic work |
| Source phones in bulk | TBD | Negotiation |
| Alternative Codec Zero sourcing | TBD | Availability dependent |

---

## Version History

- 2026-04-09: Initial estimate based on v2 PCB design with bare RP2040
