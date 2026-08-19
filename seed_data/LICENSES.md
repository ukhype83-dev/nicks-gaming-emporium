# Seed-data licences

The files in `seed_data/` originate from public-domain or openly
licensed sources. Attribution obligations below.

---

## postal_codes.tsv — GeoNames postal codes

- **Source:** http://download.geonames.org/export/zip/
- **Licence:** Creative Commons Attribution 4.0 International (CC BY 4.0)
- **URL:** https://creativecommons.org/licenses/by/4.0/

**Required attribution:**

> This work contains postal-code data from GeoNames.org
> (https://www.geonames.org/), licensed under CC BY 4.0.

Coverage: 18 countries — US, GB, DE, FR, ES, IT, NL, JP, AU, CA, BR, KR, SE, NO, DK, CH, PL, CZ.

Any redistribution of this dataset (or derivatives) must carry the
attribution and licence notice above.

---

## hardware.tsv — console / hardware catalog (V1.21.0)

- **Source:** English-language Wikipedia hardware articles (per-console
  models/revisions pages); `source_url` preserved per row
- **Licence:** CC BY-SA 4.0 (part of the main project data distribution)
- **URL:** https://creativecommons.org/licenses/by-sa/4.0/

**Scope:** 118 hardware models across 44 platforms — consoles,
handhelds, home computers, and 31 peripheral/accessory SKUs (controllers,
memory cards, headsets, chargers). Fields (model name, model number,
regional launch dates, launch price) are facts and not copyrightable
under *Feist*; the compilation tracks the Wikipedia-sourced game catalog
under CC BY-SA 4.0.

**Required attribution:**

> Hardware-catalog data is derived from Wikipedia articles under the
> Creative Commons Attribution-ShareAlike 4.0 License. Source article URLs
> are preserved in the `source_url` column of each hardware row.

See the main project `LICENSE.md` §3.1.2 for full detail.

---

## curated_duds.csv — editorial dud list

- **Source:** editorial, compiled from public knowledge of widely-
  reported game reception
- **Licence:** CC BY-SA 4.0 (part of the main project data distribution)

**Scope:** 83 titles across 26 platforms, each with a `score` anchor
(20–42) and short factual note explaining reception. Drawn from
publicly documented reviews, aggregator score patterns, and widely-
reported commercial / development failures. No proprietary data
sources (Metacritic, OpenCritic, MobyRank) were scraped; the scores
are original editorial judgements, deterministic for simulator seeding.

No external attribution required. Falls under the main project
`LICENSE.md` dual-licence.
