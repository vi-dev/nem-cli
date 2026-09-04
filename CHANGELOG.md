# Changelog

## [0.4.0](https://github.com/vi-dev/nem-cli/compare/v0.3.0...v0.4.0) (2026-09-04)


### Features

* Add nem update command ([4e95481](https://github.com/vi-dev/nem-cli/commit/4e95481ddeeec7e00eb57766b9a704be624ad88a))
* Resolve versions within dependency constraints ([77fe8a7](https://github.com/vi-dev/nem-cli/commit/77fe8a75e9e1bd8a986ee270cef393c57985df58))
* Update `nem self update` to track release channels ([ec75930](https://github.com/vi-dev/nem-cli/commit/ec75930bfe00b8ed9ee2e50c42fb0bfc5c772c76))

## [0.3.0](https://github.com/vi-dev/nem-cli/compare/v0.2.0...v0.3.0) (2026-09-01)


### Features

* Add a test key proving a package actually works ([64f9266](https://github.com/vi-dev/nem-cli/commit/64f9266fa48b9b8605218954b26364aa4c1697c8))
* Add air-gapped catalog support ([bbe7452](https://github.com/vi-dev/nem-cli/commit/bbe7452e3762c4334b8cd489b95edfa944381809))
* Add autotools and generator tools to the build image ([5d738a4](https://github.com/vi-dev/nem-cli/commit/5d738a413a984ac882ce890e5cfceaf55e26453a))
* Add rootless runtime image variant ([5f6f88d](https://github.com/vi-dev/nem-cli/commit/5f6f88d735a7217a8173b0600782739138d47ac6))
* Add short aliases for common commands ([8bbc2db](https://github.com/vi-dev/nem-cli/commit/8bbc2db877288d76b609b09f77277bb735e91fef))
* Make resolution order-independent via two-phase selection ([03e6950](https://github.com/vi-dev/nem-cli/commit/03e69501ff970d2c84fd1f8e9273908c3294b748))
* Shell completions ([4e3163f](https://github.com/vi-dev/nem-cli/commit/4e3163f25eb647933e306b7a2c888cdaf1a6fb03))
* Support directory targets in nem catalog bump ([b46b70b](https://github.com/vi-dev/nem-cli/commit/b46b70bcac4fe78d800a8e69658f7dde02cc7de8))


### Bug Fixes

* Add darwin headerpad so normalize can grow load commands ([feb4384](https://github.com/vi-dev/nem-cli/commit/feb4384ad0615dc8aef352069e8367ada02c597b))
* Add rpaths to a package's own libs during darwin normalize ([e48be8a](https://github.com/vi-dev/nem-cli/commit/e48be8a3ab5bab6d082a6231c6d1cbe52130a984))
* Serialize console narration for concurrent writers ([ae07f17](https://github.com/vi-dev/nem-cli/commit/ae07f177e21cca9160eed92f3c2ec20f72146056))
* Skip a build whose package excludes the host platform ([9b78c47](https://github.com/vi-dev/nem-cli/commit/9b78c471db3dd6320b4382eac7f246619f3cab15))


### Performance

* Open the catalog mirror once per command ([de1cd07](https://github.com/vi-dev/nem-cli/commit/de1cd07d36ba5c96633eddf44b85c0efafdf269d))

## [0.2.0](https://github.com/vi-dev/nem-cli/compare/v0.1.1...v0.2.0) (2026-08-21)


### Features

* Add catalog diff command ([1bdf7a6](https://github.com/vi-dev/nem-cli/commit/1bdf7a6fb44008a2fcc79035dcd9bf8b0ac60ecc))
* Add catalog missing command ([963b7bf](https://github.com/vi-dev/nem-cli/commit/963b7bf39ac580150925f16347d54f6e5b5496df))
* Add catalog outdated, bump, and fmt commands ([1f3a8ac](https://github.com/vi-dev/nem-cli/commit/1f3a8ac86c187faafa571c146bf748309f71f430))
* Add http version discovery scraping versions from a page ([49b51af](https://github.com/vi-dev/nem-cli/commit/49b51af4d36c731bd53f6b023c3fe308068012dc))
* Add info command showing package details and versions ([c48d539](https://github.com/vi-dev/nem-cli/commit/c48d539ca5a023d61090854af90cc0f76a0e2101))
* Add lock command and global sync scope ([5a0ef49](https://github.com/vi-dev/nem-cli/commit/5a0ef49190fca1d3443a2b102b3af96ec65fb90e))
* Add nem clean to reclaim disk space ([97002c0](https://github.com/vi-dev/nem-cli/commit/97002c0c8f41cce84623dd000b2d90321d538de2))
* Add nem self update to update the installed binary ([299c327](https://github.com/vi-dev/nem-cli/commit/299c327fa9247a17590944414d058d3f57769794))
* Compose dep prefixes, rpath-link, and cgo flags into the build env ([a5e6b8c](https://github.com/vi-dev/nem-cli/commit/a5e6b8c778976a72e52c91319e6be9136ffcf877))
* Expand templates in install action paths ([5ee6311](https://github.com/vi-dev/nem-cli/commit/5ee63110127a1fd1b144895103f70e4422cc4423))
* Filter install actions and build steps by platform ([34cb950](https://github.com/vi-dev/nem-cli/commit/34cb950e59786e243c4996c09df02d11549dc313))
* Group commands in help output ([513b99a](https://github.com/vi-dev/nem-cli/commit/513b99a4aca68909de690de44a196a051c568b0e))
* List all packages when search has no query ([eddbb85](https://github.com/vi-dev/nem-cli/commit/eddbb85fa94b13e73b9b10300ffdbf5d2ca32861))
* Normalize source build output trees ([d67a213](https://github.com/vi-dev/nem-cli/commit/d67a2131fb4edf113e3de44d4e4b6fbae5c05bef))
* Put link deps' bins on the build PATH ([414a602](https://github.com/vi-dev/nem-cli/commit/414a602cb10bb29e86fb58b6829ff633012a5c3b))
* Reject versions that are not valid OCI tags ([0667f98](https://github.com/vi-dev/nem-cli/commit/0667f98d51c22f3706e388ade09836c0622be472))
* Strip a declared suffix in github version discovery ([6d6f5b3](https://github.com/vi-dev/nem-cli/commit/6d6f5b351d3a1cee4f35cfde53ade4c6ad70bab4))
* Support gitlab and generic git version discovery sources ([d4a0749](https://github.com/vi-dev/nem-cli/commit/d4a07492c5f2ce2004a582e0bfa0b9218f496c1e))
* Support xz-compressed source tarballs ([e49e823](https://github.com/vi-dev/nem-cli/commit/e49e8232af4b142e9bb651343c65df0467b87022))
* Support zstd-compressed source tarballs ([520d456](https://github.com/vi-dev/nem-cli/commit/520d456e480cf45d35e238ad2e27aee7cfe9f01c))
* Unify extraction and support single-file compressed artifacts ([e7b03d7](https://github.com/vi-dev/nem-cli/commit/e7b03d78eeb46bd7407f30282a46fa6b829bdfb6))


### Bug Fixes

* Confine not-installed warnings to `nem status` ([7c255cd](https://github.com/vi-dev/nem-cli/commit/7c255cd86e2bea1a478653b00c09d01487108c8e))
* Consult docker credential helpers only for stored logins ([b732841](https://github.com/vi-dev/nem-cli/commit/b732841719a4f5fe3265e09914a2b0e8cad08ada))
* Keep pinned tools when unconstrained deps float to latest ([c028d29](https://github.com/vi-dev/nem-cli/commit/c028d29e26c26669ff74c292c324030e6ac3d365))
* Show composed environment in nem status ([6f3dab3](https://github.com/vi-dev/nem-cli/commit/6f3dab3d3014aaf86d74f59b96df0ab599c1d0d2))
* Take a bare repository ref in catalog publish ([0800696](https://github.com/vi-dev/nem-cli/commit/0800696653ba9c5bb4386d75c6184519e339a248))


### Refactors

* Apply cleanups across fetch, publish, and bump ([4d3cc7f](https://github.com/vi-dev/nem-cli/commit/4d3cc7f538d675aae52174819100bc4552a9d35c))
* Route all terminal output through report.Console ([703fb36](https://github.com/vi-dev/nem-cli/commit/703fb362b7fc0d3e8c40aaba47c37c0ce218607d))

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
