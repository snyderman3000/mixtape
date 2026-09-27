# Release notes

Each file here (`v0.1.2.md`, …) becomes the description of the matching GitHub release.

To release: bump `version` in `main.go`, add `docs/releases/vX.Y.Z.md`, and push to `main`.
The Release workflow then tests, builds, tags and publishes it. Without a notes file
nothing is released, so ordinary pushes never publish by accident.
