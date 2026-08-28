# ⚠ DRAFT mockups — DO NOT copy anything from these images

These four PNGs are the **original AI-generated mockups**. They are kept only as loose visual
direction (layout, density, the cream/graphite/champagne palette). **Their content is wrong for
Swift** and must not be transcribed into code, UI strings, or new mockups.

Known errors still visible in the pixels:

- "Stacked Large Object" / "STACKED OBJECT" — SLO is **Static** Large Object;
- `x-amz-meta-*` metadata rows — Swift uses `X-Object-Meta-*`;
- invented `Redundancy EC:4+2` and `Checksum CRC32C` large-object fields;
- `Multipart` / `part size` / `64 MB` large-object settings — Swift has **segments**; the chunk size is 5 GiB;
- an **ACL tab on an object** — Swift ACLs are container-level;
- "S3-compatible object stores" — Aeronautes speaks the **native Swift API**;
- cluster-capacity telemetry a tenant cannot obtain; `you@acme.com` (TempAuth is `account:user`);
- the wordmark in the pixels is wrong — it must read **Aeronautes**.

**Authoritative sources instead of these images:**

- `DESIGN.md` §4.5 — the normative correction table;
- `assets/REGENERATION.md` — Swift-correct, screen-by-screen regeneration specs.

When the mockups are regenerated from `REGENERATION.md`, place the corrected PNGs at the **`assets/`
root** (the paths `DESIGN.md` embeds) and delete this `draft/` folder.
