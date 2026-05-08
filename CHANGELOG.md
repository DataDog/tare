# Changelog

All notable changes to this project will be documented in this file.

## [0.2.0] - 2026-05-08
[0.2.0]: https://github.com/DataDog/tare/compare/v0.1.1...v0.2.0

### Added

- Show subset of stdout/stderr/contents on test failures (#7)
- Allow {empty: true} shorthand for checks (#8)
- Turn off harness for specific command tests (#9)

### Fixed

- Ensure harness files are owned by root (#5)
- Generate cross-platform toybox symlinks (#6)
- Tare.runtime per-field merge semantics (#10)

## [0.1.1] - 2026-05-07
[0.1.1]: https://github.com/DataDog/tare/compare/v0.1.0...v0.1.1

### Fixed

- Follow absolute symlink targets virtually (#3)

## [0.1.0] - 2026-05-07
[0.1.0]: https://github.com/DataDog/tare/releases/tag/v0.1.0

### Added

- Initial release
