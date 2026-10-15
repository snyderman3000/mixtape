MIXTAPE — community port deck for OnionOS (Miyoo Mini / Mini Plus)
====================================================================
Built by Claude, Anthropic's AI model, for snyderman3000.
Source, updates and bug reports: https://github.com/snyderman3000/mixtape

INSTALL
  Copy the "App" folder from this zip to the root of your SD card
  (merge it with the App folder that's already there). You'll then
  find Mixtape under Apps. Wi-Fi needs a Miyoo Mini Plus (or Flip).

CONTROLS
  D-pad up/down .... move          Left/right ... page up/down
  L1 / R1 .......... switch tab (All / Apps / Games / Homebrew / On card)
  A ................ open / install / update
  X ................ erase (only things Mixtape installed)
                     Homebrew list: switch system (All/GBA/GBC/GB/NES)
  Y ................ check for updates (list) / rescan release (detail)
                     Homebrew: sync (list) / next screenshot (detail)
  SELECT ........... Mixtape menu: update Mixtape (it also checks at
                     startup), Mixtape in Games list (to add it to Favorites)
  B ................ back / exit        MENU ... exit

WHERE THE LIST COMES FROM
  The live catalog of Miyoo Mini ports kept by Producdevity:
  https://github.com/Producdevity/MiyooMini-Ports
  Mixtape downloads each project's newest GitHub release, works out
  where it goes (App/, Roms/PORTS, ...), and installs it. If you're
  offline it uses the last catalog it saw (or the built-in copy).

  The HOMEBREW tab lists Game Boy, Game Boy Color, Game Boy Advance
  and NES homebrew from Homebrew Hub (https://hh.gbdev.io), read live
  from its public API. The first visit downloads the whole list in the
  background (about a minute); later visits only fetch what's new.
  Installing saves the ROM into Roms/GBA, Roms/GBC, Roms/GB or Roms/FC
  with its cover as box art. Erase removes it again; saves are kept.

MARKERS
  ●  installed by Mixtape         ○  found on the card (installed by hand)
  ▲NEW  an update is available    BYO  you supply the game files
  PC  has to be installed from a computer (e.g. .7z releases)

SAFETY
  * Nothing is written outside the SD card; Onion's own system files
    in .tmp_update are never replaced (only startup hooks are allowed).
  * Updates keep settings files you've edited.
  * Erase removes only the files Mixtape recorded when installing.
  * Games marked BYO still need your own legally owned game files —
    the Liner Notes for each one say where they go.

SETTINGS  (App/Mixtape/data/config.json)
  github_token  optional personal access token (no scopes needed). GitHub
                allows 60 release lookups per hour without one.
  catalog_url   use a different ports.json
  homebrew_api  use a different Homebrew Hub API address
  data/recipes.json  per-repo install overrides (same format as built-in).

LOGS: App/Mixtape/data/mixtape.log

Fonts: Share Tech Mono, VT323, Orbitron (SIL Open Font License).
CA bundle: Mozilla via curl (MPL 2.0). Licenses in the licenses folder.
