# Changelog

## [0.1.1](https://github.com/vi-dev/nem-cli/compare/v0.1.0...v0.1.1) (2026-08-14)


### Bug Fixes

* Attest a single image digest instead of every tag ([75cf3f1](https://github.com/vi-dev/nem-cli/commit/75cf3f151f00afe4ce6eda0b80f7b9376ec71e1d))

## [0.1.0](https://github.com/vi-dev/nem-cli/compare/v0.0.0...v0.1.0) (2026-08-14)


### Features

* Add catalog lint and publish ([29503b6](https://github.com/vi-dev/nem-cli/commit/29503b69a42410953abdf1e135622804693e984b))
* Add catalogs ([5138d64](https://github.com/vi-dev/nem-cli/commit/5138d64b9209b81e1f81b1ae47958e1c06e3898e))
* Add core install loop ([8d49514](https://github.com/vi-dev/nem-cli/commit/8d49514c080bf58e57b18fc16f5b25a878f068f1))
* Add environment composition and shell activation ([e5c7f18](https://github.com/vi-dev/nem-cli/commit/e5c7f18b38e66aac969a0cbe20d16dd66fcb7bf5))
* Add foundations ([c26fb30](https://github.com/vi-dev/nem-cli/commit/c26fb30f2255893c157deaf4d9d54c69cede73ef))
* Add local catalog registry dev shortcut ([343968f](https://github.com/vi-dev/nem-cli/commit/343968f2c14f81f1eb04b647f4cecaa64c636471))
* Add nem catalog disable and enable commands ([7829bd9](https://github.com/vi-dev/nem-cli/commit/7829bd9d68f6624c4675e765ad28d2ac96fa5474))
* Add release pipeline and install script ([ff994e4](https://github.com/vi-dev/nem-cli/commit/ff994e49f77714ad829dfde233c8fd0618279e59))
* Bootstrap cli with cobra and version command ([d9dfcb2](https://github.com/vi-dev/nem-cli/commit/d9dfcb2f2909e6fdd53afc7e1a3ffe7f3f1fd787))
* First-class source-built library packages ([a2d651c](https://github.com/vi-dev/nem-cli/commit/a2d651cb20688579953d813fba99d41d32ddf9b6))
* Make disabled catalogs inert in resolution and sync ([ca97423](https://github.com/vi-dev/nem-cli/commit/ca97423c017dee39b23fcbfbb39e2791e29df909))
* Add nem catalog build --push ([0da2edf](https://github.com/vi-dev/nem-cli/commit/0da2edf0accaeb10fe9daf8dd6ea60998be46e19))
* Add nem catalog build (local source-build engine) ([ddca569](https://github.com/vi-dev/nem-cli/commit/ddca56947ecaec04f0daff7003aa94dbf7109ae5))
* Scope nem status to project or global (-g) ([94667cb](https://github.com/vi-dev/nem-cli/commit/94667cb05777d49b56667c387344d566cd51e994))
* Support bzip2 source tarballs in catalog build ([24bbb15](https://github.com/vi-dev/nem-cli/commit/24bbb15d59b690c4fb50a61541ad22ec5e50542a))


### Bug Fixes

* Bake a correct $ORIGIN rpath on Linux source builds ([f0471a2](https://github.com/vi-dev/nem-cli/commit/f0471a280690a103e8c22fac758b254a7f90b917))
* Require go1.26.6 for stdlib security fixes ([3802899](https://github.com/vi-dev/nem-cli/commit/3802899bc8fb5e3ed585b2bc58c064c97f505365))
* Skip unsynced catalogs during first-match resolution ([6d46c5a](https://github.com/vi-dev/nem-cli/commit/6d46c5acb302e94b5db52761d1da696bcd7a9fda))
* Write resolved version to nem.toml on version-less use ([f4f8e9f](https://github.com/vi-dev/nem-cli/commit/f4f8e9f10a375d898243e1040c25fa1039d9dd7d))
