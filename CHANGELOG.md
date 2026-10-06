# Changelog

## [0.4.6](https://github.com/ildarbinanas-design/env-vault/compare/v0.4.5...v0.4.6) (2026-10-06)


### Bug Fixes

* **deps:** update wincred to v1.2.3 and reject D-Bus downgrade ([#144](https://github.com/ildarbinanas-design/env-vault/issues/144)) ([71d4dbb](https://github.com/ildarbinanas-design/env-vault/commit/71d4dbb1ee77793aeecea38fb1ac9fcd3f98e425))
* **exec:** preserve success when metadata output fails ([#140](https://github.com/ildarbinanas-design/env-vault/issues/140)) ([260f99b](https://github.com/ildarbinanas-design/env-vault/commit/260f99b91f8157bf7926f884acca7ecf2e1cddf9))

## [0.4.5](https://github.com/ildarbinanas-design/env-vault/compare/v0.4.4...v0.4.5) (2026-10-03)


### Bug Fixes

* preserve exports and harden secret input handling ([#135](https://github.com/ildarbinanas-design/env-vault/issues/135)) ([c23cf6f](https://github.com/ildarbinanas-design/env-vault/commit/c23cf6f47afea61cb3cdc5cb380007a823b1cfb5))

## [0.4.4](https://github.com/ildarbinanas-design/env-vault/compare/v0.4.3...v0.4.4) (2026-10-03)


### Bug Fixes

* preserve local data and reject backend failures ([#132](https://github.com/ildarbinanas-design/env-vault/issues/132)) ([0e52d87](https://github.com/ildarbinanas-design/env-vault/commit/0e52d87b5910cc0eecc22a6e467098c343921cdf))

## [0.4.3](https://github.com/ildarbinanas-design/env-vault/compare/v0.4.2...v0.4.3) (2026-10-02)


### Bug Fixes

* **secretstore:** preserve native identity and validate imports before writes ([#129](https://github.com/ildarbinanas-design/env-vault/issues/129)) ([e9aca77](https://github.com/ildarbinanas-design/env-vault/commit/e9aca77f0164fb4155ee8246d239f8de00d21e0b))

## [0.4.2](https://github.com/ildarbinanas-design/env-vault/compare/v0.4.1...v0.4.2) (2026-10-02)


### Bug Fixes

* **bundle:** reject oversized exports and close review regressions ([9c15037](https://github.com/ildarbinanas-design/env-vault/commit/9c15037c5e00227cc4341783098dc80bf266eef2))

## [0.4.1](https://github.com/ildarbinanas-design/env-vault/compare/v0.4.0...v0.4.1) (2026-09-30)


### Bug Fixes

* **deps:** build with Go 1.26.8 ([#116](https://github.com/ildarbinanas-design/env-vault/issues/116)) ([8932f3d](https://github.com/ildarbinanas-design/env-vault/commit/8932f3dcdea5f8cca77daf0e76270c27b5db8ca1))
* **output:** write --output atomically and record failed commands ([#121](https://github.com/ildarbinanas-design/env-vault/issues/121)) ([eb1ce54](https://github.com/ildarbinanas-design/env-vault/commit/eb1ce54da2d338807c434ae01db7a673e12f0906))
* **secret:** refuse --stdin from a terminal, count passphrase characters ([#122](https://github.com/ildarbinanas-design/env-vault/issues/122)) ([ee60230](https://github.com/ildarbinanas-design/env-vault/commit/ee60230fdbf282b6cd86e2b7fde36c786badf303))
* **secret:** say what a failed --verify write did ([#125](https://github.com/ildarbinanas-design/env-vault/issues/125)) ([729fd39](https://github.com/ildarbinanas-design/env-vault/commit/729fd39defc64653d75411d761c267e984e77ef1))
* **secretstore:** refuse a service name with a slash on pass ([#124](https://github.com/ildarbinanas-design/env-vault/issues/124)) ([19c7493](https://github.com/ildarbinanas-design/env-vault/commit/19c749359855ed33543963524de47e9e15582b91))
* **secretstore:** report the Windows Credential Manager size limit ([#123](https://github.com/ildarbinanas-design/env-vault/issues/123)) ([41b7183](https://github.com/ildarbinanas-design/env-vault/commit/41b718323f68c19f96fb4b7f8b63f1a17cb7935c))

## [0.4.0](https://github.com/ildarbinanas-design/env-vault/compare/v0.3.4...v0.4.0) (2026-09-29)


### Features

* **cli:** print the commit and date in --version ([#111](https://github.com/ildarbinanas-design/env-vault/issues/111)) ([d76b6de](https://github.com/ildarbinanas-design/env-vault/commit/d76b6de1bd4132d89b3428a30af694243a70f071))

## [0.3.4](https://github.com/ildarbinanas-design/env-vault/compare/v0.3.3...v0.3.4) (2026-09-27)


### Features

* add secrets import/export to encrypted transfer container ([#78](https://github.com/ildarbinanas-design/env-vault/issues/78)) ([dd32cb0](https://github.com/ildarbinanas-design/env-vault/commit/dd32cb00c5ec36cfa69a78607f3257cc2f11973d))
* govern Actions artifact lifecycle ([#60](https://github.com/ildarbinanas-design/env-vault/issues/60)) ([7d36786](https://github.com/ildarbinanas-design/env-vault/commit/7d367862ce409777689083a0cfa56d292c0459e0))
* implement env-vault local MVP ([9dbfe13](https://github.com/ildarbinanas-design/env-vault/commit/9dbfe1319fcd112a858fc8c6e77aa7361c958a3e))
* **secret:** report record IDs, overwrites and verified writes ([#87](https://github.com/ildarbinanas-design/env-vault/issues/87)) ([ed8e40f](https://github.com/ildarbinanas-design/env-vault/commit/ed8e40f69781385e1e637b622f7da13cb915a065)), closes [#77](https://github.com/ildarbinanas-design/env-vault/issues/77)


### Bug Fixes

* **ci:** make E2E reporter bootstrap deterministic ([#36](https://github.com/ildarbinanas-design/env-vault/issues/36)) ([50f927c](https://github.com/ildarbinanas-design/env-vault/commit/50f927ccc8a0ba6f7921f8fc5f27b31a0113141c))
* **cli:** resolve version from build info and support --version flag ([#7](https://github.com/ildarbinanas-design/env-vault/issues/7)) ([7656275](https://github.com/ildarbinanas-design/env-vault/commit/765627566f1d5ba175de017fe8ef3614a0408453))
* **config:** stabilize Windows saves and automate releases ([#17](https://github.com/ildarbinanas-design/env-vault/issues/17)) ([8dfffca](https://github.com/ildarbinanas-design/env-vault/commit/8dfffca1b30db1a09c495b29064d599e9a3d556e))
* **e2e:** seal release-version contract normalization ([#24](https://github.com/ildarbinanas-design/env-vault/issues/24)) ([e298cd0](https://github.com/ildarbinanas-design/env-vault/commit/e298cd0d473c41654fb6464f16f11ee477e60547))
* **env-vault:** enable darwin cgo builds and pass backend ([#2](https://github.com/ildarbinanas-design/env-vault/issues/2)) ([e5a7ca8](https://github.com/ildarbinanas-design/env-vault/commit/e5a7ca83e775c660a8e65cbf2794306d6dcb4463))
* **exec:** die by the child's signal and stop doubling terminal interrupts ([#95](https://github.com/ildarbinanas-design/env-vault/issues/95)) ([9b30263](https://github.com/ildarbinanas-design/env-vault/commit/9b302633448c580a610a3e7e90bc81fd7358942d))
* **exec:** keep SIGHUP and SIGINT ignored for the child under nohup ([#101](https://github.com/ildarbinanas-design/env-vault/issues/101)) ([a0ca4ba](https://github.com/ildarbinanas-design/env-vault/commit/a0ca4ba144cebcd54ccc5ea879585e46de44f8b6))
* **keyring:** scope pass backend to env-vault namespace ([#4](https://github.com/ildarbinanas-design/env-vault/issues/4)) ([4a8b116](https://github.com/ildarbinanas-design/env-vault/commit/4a8b11697d93829c364e0807d83fc87df2a2fd5a))
* **output:** stop rewriting output around stored secret values ([#90](https://github.com/ildarbinanas-design/env-vault/issues/90)) ([590f8ca](https://github.com/ildarbinanas-design/env-vault/commit/590f8ca90bd9590f50b59cbf5d2e3a58ba6247cb))
* **release:** accept GitHub's unattributed-changes ruleset parameter ([#85](https://github.com/ildarbinanas-design/env-vault/issues/85)) ([c9c9d53](https://github.com/ildarbinanas-design/env-vault/commit/c9c9d53cd168c8e7ca5515501a2ddaf35f7b279f))
* **release:** accept the deleted planning App's ghost author on PR [#31](https://github.com/ildarbinanas-design/env-vault/issues/31) ([#103](https://github.com/ildarbinanas-design/env-vault/issues/103)) ([4d40a53](https://github.com/ildarbinanas-design/env-vault/commit/4d40a5301104604aa91557d679623ae245b0bb6a))
* **release:** add deterministic release diagnostics ([#20](https://github.com/ildarbinanas-design/env-vault/issues/20)) ([0eef845](https://github.com/ildarbinanas-design/env-vault/commit/0eef84548b802a89f98c08744b5b51aac3d543ad))
* **release:** align checksum line ending contract ([#30](https://github.com/ildarbinanas-design/env-vault/issues/30)) ([c7d3cd8](https://github.com/ildarbinanas-design/env-vault/commit/c7d3cd80dcdca69824400649431b791e3a0c9271))
* **release:** centralize GitHub transport ([#47](https://github.com/ildarbinanas-design/env-vault/issues/47)) ([2c6932f](https://github.com/ildarbinanas-design/env-vault/commit/2c6932fe10a70b3a7cf22e751965af8aca52cb6b))
* **release:** compact and anchor durable evidence ([#48](https://github.com/ildarbinanas-design/env-vault/issues/48)) ([fa5e3fd](https://github.com/ildarbinanas-design/env-vault/commit/fa5e3fdfe75c956dbd9e4f70484de1f0ec81de3a))
* **release:** complete recovery and document operations ([#38](https://github.com/ildarbinanas-design/env-vault/issues/38)) ([c92467f](https://github.com/ildarbinanas-design/env-vault/commit/c92467faecf165795ab842dddc372ad97b183a8c))
* **release:** harden durable evidence identity ([#41](https://github.com/ildarbinanas-design/env-vault/issues/41)) ([8bf408a](https://github.com/ildarbinanas-design/env-vault/commit/8bf408acf804d8ef16644d9015928b3b1872e75b))
* **release:** isolate publisher asset inventory ([#27](https://github.com/ildarbinanas-design/env-vault/issues/27)) ([5a37ea6](https://github.com/ildarbinanas-design/env-vault/commit/5a37ea653b40b583c4fee2153dfcf872216d0dfd))
* **release:** locate nested promotion manifests ([#25](https://github.com/ildarbinanas-design/env-vault/issues/25)) ([c9ac7cc](https://github.com/ildarbinanas-design/env-vault/commit/c9ac7cced671e537441370e0b33da46448eb1de8))
* **release:** parse Homebrew health state exactly ([#37](https://github.com/ildarbinanas-design/env-vault/issues/37)) ([d4c8ac7](https://github.com/ildarbinanas-design/env-vault/commit/d4c8ac7c7364e8bc7f85a40bc8fb162bb506a48f))
* **release:** preserve read-only App audit ([#19](https://github.com/ildarbinanas-design/env-vault/issues/19)) ([33ae082](https://github.com/ildarbinanas-design/env-vault/commit/33ae082bc78d24184624de6cf88fb224e93182f4))
* **release:** promote exact CI artifacts ([#22](https://github.com/ildarbinanas-design/env-vault/issues/22)) ([d0c6a32](https://github.com/ildarbinanas-design/env-vault/commit/d0c6a320bade9d09b77ecfcca89d23daded57da6))
* **release:** publish releases from a draft after all assets upload ([#96](https://github.com/ildarbinanas-design/env-vault/issues/96)) ([d4aa425](https://github.com/ildarbinanas-design/env-vault/commit/d4aa425bf70b6e9c4f11cacb3066e66138207e60))
* **release:** record authorization before merge ([#32](https://github.com/ildarbinanas-design/env-vault/issues/32)) ([0550a2b](https://github.com/ildarbinanas-design/env-vault/commit/0550a2b14c4d17e26e3d5a017c9118e018f3837c))
* **release:** recover abandoned v0.0.12 proposal ([#34](https://github.com/ildarbinanas-design/env-vault/issues/34)) ([5e3b94e](https://github.com/ildarbinanas-design/env-vault/commit/5e3b94ecd6f5b41f3f63bb2c878c7e65844da282))
* **release:** recover empty release assets ([#49](https://github.com/ildarbinanas-design/env-vault/issues/49)) ([6989b73](https://github.com/ildarbinanas-design/env-vault/commit/6989b737c0e0a7407b5b7949840b0e139f406f16))
* **release:** recover Homebrew publication safely ([#52](https://github.com/ildarbinanas-design/env-vault/issues/52)) ([ce1ba71](https://github.com/ildarbinanas-design/env-vault/commit/ce1ba7186a4d3133fb04075f275f06e6042c0ccb))
* **release:** require evidence branch bootstrap ([#44](https://github.com/ildarbinanas-design/env-vault/issues/44)) ([95a6293](https://github.com/ildarbinanas-design/env-vault/commit/95a62938edfa6206d1f1bcc34d0fefdbdff1bd5e))
* **release:** require GET for workflow queries ([#51](https://github.com/ildarbinanas-design/env-vault/issues/51)) ([2f92b66](https://github.com/ildarbinanas-design/env-vault/commit/2f92b66ee02eb77bb7bc6e628c4c2766c889ff20))
* **release:** retry read-only GitHub observations ([#33](https://github.com/ildarbinanas-design/env-vault/issues/33)) ([e55cfdb](https://github.com/ildarbinanas-design/env-vault/commit/e55cfdbc3ba68e29eb68f0d438bd55cd3a93b394))
* **release:** share README version contract ([#29](https://github.com/ildarbinanas-design/env-vault/issues/29)) ([f287f20](https://github.com/ildarbinanas-design/env-vault/commit/f287f2078ff59b18c0933e28a999a49dfb9f1fd8))
* **release:** verify read-only bypass state ([#23](https://github.com/ildarbinanas-design/env-vault/issues/23)) ([0b21e66](https://github.com/ildarbinanas-design/env-vault/commit/0b21e66664137a2944049633078a9658b4703625))
* **release:** verify wrapped evidence blobs ([#43](https://github.com/ildarbinanas-design/env-vault/issues/43)) ([dc2bb7e](https://github.com/ildarbinanas-design/env-vault/commit/dc2bb7e989179698655e6dd139e3b0f94277d314))
* **secretstore:** fail clearly on refused or unanswered keychain calls ([#94](https://github.com/ildarbinanas-design/env-vault/issues/94)) ([8c6591f](https://github.com/ildarbinanas-design/env-vault/commit/8c6591f3a267b1133d446768b5f0b95e0cef42f8))
* **secret:** trim a CRLF line ending from --stdin input ([#91](https://github.com/ildarbinanas-design/env-vault/issues/91)) ([5eb9ed6](https://github.com/ildarbinanas-design/env-vault/commit/5eb9ed69e7657be06244aa23a7931084dab0a45d))
* **security:** harden runtime and Homebrew release contract ([#13](https://github.com/ildarbinanas-design/env-vault/issues/13)) ([4fbae38](https://github.com/ildarbinanas-design/env-vault/commit/4fbae380747e75a1f59498adbd76ccf5791e0480))


### Build System

* **deps:** bump github.com/dvsekhvalnov/jose2go from 1.5.0 to 1.7.0 ([00d11c2](https://github.com/ildarbinanas-design/env-vault/commit/00d11c2726949a65f00416c449ce6e1851a07456))
* **deps:** bump go-modules-minor-patch group (retry of [#81](https://github.com/ildarbinanas-design/env-vault/issues/81) on fresh base) ([5234501](https://github.com/ildarbinanas-design/env-vault/commit/523450175583d5ee1c9950f1626afa04c83f9bdc))
* **deps:** bump the github-actions-minor-patch group across 1 directory with 2 updates ([#65](https://github.com/ildarbinanas-design/env-vault/issues/65)) ([2e6c8ca](https://github.com/ildarbinanas-design/env-vault/commit/2e6c8ca135e20e1f2840e9c688aea27bc5450ed8))
* migrate env-vault to Go 1.26.5 ([#16](https://github.com/ildarbinanas-design/env-vault/issues/16)) ([a4e9a51](https://github.com/ildarbinanas-design/env-vault/commit/a4e9a5169959666a50f5022194d0b802cf3edac8))


### Continuous Integration

* add binary build workflow ([cefbdb8](https://github.com/ildarbinanas-design/env-vault/commit/cefbdb8fe28c7fc896e3afeeba8c9881b5f9946d))
* auto-update Homebrew tap formula on release ([#5](https://github.com/ildarbinanas-design/env-vault/issues/5)) ([15970bb](https://github.com/ildarbinanas-design/env-vault/commit/15970bb29fcdd9dd14584022cefdfbe7d4da5e64))
* fix release publishing without checkout ([b9dd882](https://github.com/ildarbinanas-design/env-vault/commit/b9dd8826b3dca3a0f638df39797cb13d1eb10aa5))
* harden release workflow for v0.0.5 ([#8](https://github.com/ildarbinanas-design/env-vault/issues/8)) ([1d927ce](https://github.com/ildarbinanas-design/env-vault/commit/1d927ce2828153e87399749b48656d8dbc9ce1f4))
* **release:** drop the append-only release-evidence ledger ([#71](https://github.com/ildarbinanas-design/env-vault/issues/71)) ([caa8724](https://github.com/ildarbinanas-design/env-vault/commit/caa87245113dcd164536a18277a988476521fe35))
* **release:** drop the bespoke provenance/SBOM contour ([#74](https://github.com/ildarbinanas-design/env-vault/issues/74)) ([f24db42](https://github.com/ildarbinanas-design/env-vault/commit/f24db42bf2c5fb3306fb3d3e1446a2371e611a52))
* **release:** drop the byte-exact release confirmation ceremony ([#72](https://github.com/ildarbinanas-design/env-vault/issues/72)) ([36bba6e](https://github.com/ildarbinanas-design/env-vault/commit/36bba6e9b87b78cf6be65c269a75d2268ce5946e))
* **release:** drop the dead release-metrics tooling ([#75](https://github.com/ildarbinanas-design/env-vault/issues/75)) ([7c964aa](https://github.com/ildarbinanas-design/env-vault/commit/7c964aa2b603f0613a6763c0cb4b4b2eeaff0f6a))
* **release:** drop the versioned release-contract dimension ([#73](https://github.com/ildarbinanas-design/env-vault/issues/73)) ([a10c0c4](https://github.com/ildarbinanas-design/env-vault/commit/a10c0c4a9c3f86b3da7d42fadef2fb199facea46))
* **release:** replace release GitHub Apps with scoped tokens ([#70](https://github.com/ildarbinanas-design/env-vault/issues/70)) ([c8fb608](https://github.com/ildarbinanas-design/env-vault/commit/c8fb60845b2ef20a4215ff99ac8aeff79861c92f))
* smoke-test the real OS secret stores in the native jobs ([#98](https://github.com/ildarbinanas-design/env-vault/issues/98)) ([0ae0e12](https://github.com/ildarbinanas-design/env-vault/commit/0ae0e122118711c5d36e40fc09b00aaa26e78541))


### Documentation

* add Install section with Homebrew tap and manual download ([#6](https://github.com/ildarbinanas-design/env-vault/issues/6)) ([05a2a8c](https://github.com/ildarbinanas-design/env-vault/commit/05a2a8c6f5de58c6f68c2d8fb4e4bab155fe1ee6))
* add release operations journal ([#54](https://github.com/ildarbinanas-design/env-vault/issues/54)) ([c2658bc](https://github.com/ildarbinanas-design/env-vault/commit/c2658bc9ba5a6d83578b04bebb6ae22b9505daea))
* **adr:** decide against code signing; scope macOS installs to Homebrew ([#76](https://github.com/ildarbinanas-design/env-vault/issues/76)) ([b78756e](https://github.com/ildarbinanas-design/env-vault/commit/b78756e249e026ef0f468d06e25f65c912354ae3))
* **adr:** freeze release ceremony, require PersonalOS link ([#66](https://github.com/ildarbinanas-design/env-vault/issues/66)) ([f89419f](https://github.com/ildarbinanas-design/env-vault/commit/f89419f87213db9267dd7f45df0e8b7ff183c6e0))
* **agents:** state what actually enforces the working mode ([#97](https://github.com/ildarbinanas-design/env-vault/issues/97)) ([dfd2d3f](https://github.com/ildarbinanas-design/env-vault/commit/dfd2d3fa4e7bb8c15ea7503645f6b28fa5d2bf27))
* **env-vault:** sync docs for v0.0.2 release ([#3](https://github.com/ildarbinanas-design/env-vault/issues/3)) ([595bf4f](https://github.com/ildarbinanas-design/env-vault/commit/595bf4fa7ca6a7346400e2243bc3b678f6767c5b))
* **evidence:** make release facts durable ([#12](https://github.com/ildarbinanas-design/env-vault/issues/12)) ([859cbfe](https://github.com/ildarbinanas-design/env-vault/commit/859cbfebf6b6b3ed84408100741ea5bcf5df0ee1))
* **evidence:** record healthy v0.0.6 release ([#11](https://github.com/ildarbinanas-design/env-vault/issues/11)) ([524485c](https://github.com/ildarbinanas-design/env-vault/commit/524485c85e22f084a656d3e90b03764a91ee62fa))
* freeze release refactor baseline ([#45](https://github.com/ildarbinanas-design/env-vault/issues/45)) ([114ab3e](https://github.com/ildarbinanas-design/env-vault/commit/114ab3e35b6948c0d40e5b7f9c97b867f32cf5eb))
* **plan:** resolve item 13, add Actions/UX analysis workstream ([#68](https://github.com/ildarbinanas-design/env-vault/issues/68)) ([fab51b8](https://github.com/ildarbinanas-design/env-vault/commit/fab51b800c103e469c87a769ff9836e67815de37))
* preserve Actions artifact cleanup manifest ([#61](https://github.com/ildarbinanas-design/env-vault/issues/61)) ([4c2ee80](https://github.com/ildarbinanas-design/env-vault/commit/4c2ee8070c69f3d66a4103505c4efb114c3a8931))
* **readme:** explain the macOS Keychain prompt after an upgrade ([#92](https://github.com/ildarbinanas-design/env-vault/issues/92)) ([94ee5cd](https://github.com/ildarbinanas-design/env-vault/commit/94ee5cd761fcacf1a3660fed149a3cd876fa315f))
* record Actions artifact cleanup ([#62](https://github.com/ildarbinanas-design/env-vault/issues/62)) ([b8bda39](https://github.com/ildarbinanas-design/env-vault/commit/b8bda393a634924f4b84a5d8c98a27051880310c))
* record delayed Actions billing verification ([#63](https://github.com/ildarbinanas-design/env-vault/issues/63)) ([b48fe6a](https://github.com/ildarbinanas-design/env-vault/commit/b48fe6a16c4201190d55254c550d53fbef3adc7b))
* record GitHub publication evidence ([4b1ee07](https://github.com/ildarbinanas-design/env-vault/commit/4b1ee078809c345ac8617a41f26e728152c9f8ba))
* record v0.0.1 release verification ([6a89c58](https://github.com/ildarbinanas-design/env-vault/commit/6a89c58fe74bdd46c79f21814533b69a5cd8af8b))
* **release:** record v0.0.18 and plan artifact cleanup ([#56](https://github.com/ildarbinanas-design/env-vault/issues/56)) ([398b084](https://github.com/ildarbinanas-design/env-vault/commit/398b084b2f227ef5323f34b6dda15db8cd5d1256))
* resolve governance-document audit (9/9 questions) ([#67](https://github.com/ildarbinanas-design/env-vault/issues/67)) ([68fe77f](https://github.com/ildarbinanas-design/env-vault/commit/68fe77f931a185e71165183bd9e94589376ae86c))
* sync third-party notices with go.mod; isolate x/crypto in Dependabot ([#84](https://github.com/ildarbinanas-design/env-vault/issues/84)) ([79ef830](https://github.com/ildarbinanas-design/env-vault/commit/79ef830d3d39b8c8464249ce22c520ee5b5e0284))
* trim execution plan + DevSecOps provenance/SBOM backlog item ([#69](https://github.com/ildarbinanas-design/env-vault/issues/69)) ([34aa08b](https://github.com/ildarbinanas-design/env-vault/commit/34aa08bbbf1f431f9bb8d77a427096ce1dae3cec))


### Tests

* add unit tests for internal/errors package ([10715bb](https://github.com/ildarbinanas-design/env-vault/commit/10715bb962e9a0ccc774f827c0c0ece544a04b74))
* **e2e:** establish Go 1.22 binary baseline ([#15](https://github.com/ildarbinanas-design/env-vault/issues/15)) ([7a044bd](https://github.com/ildarbinanas-design/env-vault/commit/7a044bdbf73aa592016bbb3a02d81f314f08fe63))
* **release:** accept forward manifest versions ([#40](https://github.com/ildarbinanas-design/env-vault/issues/40)) ([accd9fa](https://github.com/ildarbinanas-design/env-vault/commit/accd9fa79aa1378f012f83962e07d11ab20597c5))


### Refactoring

* **release:** centralize operational contract ([#53](https://github.com/ildarbinanas-design/env-vault/issues/53)) ([0d87427](https://github.com/ildarbinanas-design/env-vault/commit/0d874277aad3bbfa21b12296d61df8a7f770d622))

## [0.3.3](https://github.com/ildarbinanas-design/env-vault/compare/v0.3.2...v0.3.3) (2026-09-27)


### Bug Fixes

* **exec:** keep SIGHUP and SIGINT ignored for the child under nohup ([#101](https://github.com/ildarbinanas-design/env-vault/issues/101)) ([a0ca4ba](https://github.com/ildarbinanas-design/env-vault/commit/a0ca4ba144cebcd54ccc5ea879585e46de44f8b6))

## [0.3.2](https://github.com/ildarbinanas-design/env-vault/compare/v0.3.1...v0.3.2) (2026-09-27)


### Bug Fixes

* **release:** publish releases from a draft after all assets upload ([#96](https://github.com/ildarbinanas-design/env-vault/issues/96)) ([d4aa425](https://github.com/ildarbinanas-design/env-vault/commit/d4aa425bf70b6e9c4f11cacb3066e66138207e60))


### Continuous Integration

* smoke-test the real OS secret stores in the native jobs ([#98](https://github.com/ildarbinanas-design/env-vault/issues/98)) ([0ae0e12](https://github.com/ildarbinanas-design/env-vault/commit/0ae0e122118711c5d36e40fc09b00aaa26e78541))


### Documentation

* **agents:** state what actually enforces the working mode ([#97](https://github.com/ildarbinanas-design/env-vault/issues/97)) ([dfd2d3f](https://github.com/ildarbinanas-design/env-vault/commit/dfd2d3fa4e7bb8c15ea7503645f6b28fa5d2bf27))

## [0.3.1](https://github.com/ildarbinanas-design/env-vault/compare/v0.3.0...v0.3.1) (2026-09-27)


### Bug Fixes

* **exec:** die by the child's signal and stop doubling terminal interrupts ([#95](https://github.com/ildarbinanas-design/env-vault/issues/95)) ([9b30263](https://github.com/ildarbinanas-design/env-vault/commit/9b302633448c580a610a3e7e90bc81fd7358942d))
* **output:** stop rewriting output around stored secret values ([#90](https://github.com/ildarbinanas-design/env-vault/issues/90)) ([590f8ca](https://github.com/ildarbinanas-design/env-vault/commit/590f8ca90bd9590f50b59cbf5d2e3a58ba6247cb))
* **secretstore:** fail clearly on refused or unanswered keychain calls ([#94](https://github.com/ildarbinanas-design/env-vault/issues/94)) ([8c6591f](https://github.com/ildarbinanas-design/env-vault/commit/8c6591f3a267b1133d446768b5f0b95e0cef42f8))
* **secret:** trim a CRLF line ending from --stdin input ([#91](https://github.com/ildarbinanas-design/env-vault/issues/91)) ([5eb9ed6](https://github.com/ildarbinanas-design/env-vault/commit/5eb9ed69e7657be06244aa23a7931084dab0a45d))


### Documentation

* **readme:** explain the macOS Keychain prompt after an upgrade ([#92](https://github.com/ildarbinanas-design/env-vault/issues/92)) ([94ee5cd](https://github.com/ildarbinanas-design/env-vault/commit/94ee5cd761fcacf1a3660fed149a3cd876fa315f))

## [0.3.0](https://github.com/ildarbinanas-design/env-vault/compare/v0.2.1...v0.3.0) (2026-09-26)


### Features

* **secret:** report record IDs, overwrites and verified writes ([#87](https://github.com/ildarbinanas-design/env-vault/issues/87)) ([ed8e40f](https://github.com/ildarbinanas-design/env-vault/commit/ed8e40f69781385e1e637b622f7da13cb915a065)), closes [#77](https://github.com/ildarbinanas-design/env-vault/issues/77)

## [0.2.1](https://github.com/ildarbinanas-design/env-vault/compare/v0.2.0...v0.2.1) (2026-09-25)


### Bug Fixes

* **release:** accept GitHub's unattributed-changes ruleset parameter ([#85](https://github.com/ildarbinanas-design/env-vault/issues/85)) ([c9c9d53](https://github.com/ildarbinanas-design/env-vault/commit/c9c9d53cd168c8e7ca5515501a2ddaf35f7b279f))


### Build System

* **deps:** bump go-modules-minor-patch group (retry of [#81](https://github.com/ildarbinanas-design/env-vault/issues/81) on fresh base) ([5234501](https://github.com/ildarbinanas-design/env-vault/commit/523450175583d5ee1c9950f1626afa04c83f9bdc))


### Documentation

* sync third-party notices with go.mod; isolate x/crypto in Dependabot ([#84](https://github.com/ildarbinanas-design/env-vault/issues/84)) ([79ef830](https://github.com/ildarbinanas-design/env-vault/commit/79ef830d3d39b8c8464249ce22c520ee5b5e0284))


### Tests

* add unit tests for internal/errors package ([10715bb](https://github.com/ildarbinanas-design/env-vault/commit/10715bb962e9a0ccc774f827c0c0ece544a04b74))

## [0.2.0](https://github.com/ildarbinanas-design/env-vault/compare/v0.1.0...v0.2.0) (2026-08-15)


### Features

* add secrets import/export to encrypted transfer container ([#78](https://github.com/ildarbinanas-design/env-vault/issues/78)) ([dd32cb0](https://github.com/ildarbinanas-design/env-vault/commit/dd32cb00c5ec36cfa69a78607f3257cc2f11973d))


### Build System

* **deps:** bump the github-actions-minor-patch group across 1 directory with 2 updates ([#65](https://github.com/ildarbinanas-design/env-vault/issues/65)) ([2e6c8ca](https://github.com/ildarbinanas-design/env-vault/commit/2e6c8ca135e20e1f2840e9c688aea27bc5450ed8))


### Continuous Integration

* **release:** drop the append-only release-evidence ledger ([#71](https://github.com/ildarbinanas-design/env-vault/issues/71)) ([caa8724](https://github.com/ildarbinanas-design/env-vault/commit/caa87245113dcd164536a18277a988476521fe35))
* **release:** drop the bespoke provenance/SBOM contour ([#74](https://github.com/ildarbinanas-design/env-vault/issues/74)) ([f24db42](https://github.com/ildarbinanas-design/env-vault/commit/f24db42bf2c5fb3306fb3d3e1446a2371e611a52))
* **release:** drop the byte-exact release confirmation ceremony ([#72](https://github.com/ildarbinanas-design/env-vault/issues/72)) ([36bba6e](https://github.com/ildarbinanas-design/env-vault/commit/36bba6e9b87b78cf6be65c269a75d2268ce5946e))
* **release:** drop the dead release-metrics tooling ([#75](https://github.com/ildarbinanas-design/env-vault/issues/75)) ([7c964aa](https://github.com/ildarbinanas-design/env-vault/commit/7c964aa2b603f0613a6763c0cb4b4b2eeaff0f6a))
* **release:** drop the versioned release-contract dimension ([#73](https://github.com/ildarbinanas-design/env-vault/issues/73)) ([a10c0c4](https://github.com/ildarbinanas-design/env-vault/commit/a10c0c4a9c3f86b3da7d42fadef2fb199facea46))
* **release:** replace release GitHub Apps with scoped tokens ([#70](https://github.com/ildarbinanas-design/env-vault/issues/70)) ([c8fb608](https://github.com/ildarbinanas-design/env-vault/commit/c8fb60845b2ef20a4215ff99ac8aeff79861c92f))


### Documentation

* **adr:** decide against code signing; scope macOS installs to Homebrew ([#76](https://github.com/ildarbinanas-design/env-vault/issues/76)) ([b78756e](https://github.com/ildarbinanas-design/env-vault/commit/b78756e249e026ef0f468d06e25f65c912354ae3))
* **adr:** freeze release ceremony, require PersonalOS link ([#66](https://github.com/ildarbinanas-design/env-vault/issues/66)) ([f89419f](https://github.com/ildarbinanas-design/env-vault/commit/f89419f87213db9267dd7f45df0e8b7ff183c6e0))
* **plan:** resolve item 13, add Actions/UX analysis workstream ([#68](https://github.com/ildarbinanas-design/env-vault/issues/68)) ([fab51b8](https://github.com/ildarbinanas-design/env-vault/commit/fab51b800c103e469c87a769ff9836e67815de37))
* record delayed Actions billing verification ([#63](https://github.com/ildarbinanas-design/env-vault/issues/63)) ([b48fe6a](https://github.com/ildarbinanas-design/env-vault/commit/b48fe6a16c4201190d55254c550d53fbef3adc7b))
* resolve governance-document audit (9/9 questions) ([#67](https://github.com/ildarbinanas-design/env-vault/issues/67)) ([68fe77f](https://github.com/ildarbinanas-design/env-vault/commit/68fe77f931a185e71165183bd9e94589376ae86c))
* trim execution plan + DevSecOps provenance/SBOM backlog item ([#69](https://github.com/ildarbinanas-design/env-vault/issues/69)) ([34aa08b](https://github.com/ildarbinanas-design/env-vault/commit/34aa08bbbf1f431f9bb8d77a427096ce1dae3cec))

## [0.1.0](https://github.com/ildarbinanas-design/env-vault/compare/v0.0.18...v0.1.0) (2026-07-20)


### Features

* govern Actions artifact lifecycle ([#60](https://github.com/ildarbinanas-design/env-vault/issues/60)) ([7d36786](https://github.com/ildarbinanas-design/env-vault/commit/7d367862ce409777689083a0cfa56d292c0459e0))


### Documentation

* preserve Actions artifact cleanup manifest ([#61](https://github.com/ildarbinanas-design/env-vault/issues/61)) ([4c2ee80](https://github.com/ildarbinanas-design/env-vault/commit/4c2ee8070c69f3d66a4103505c4efb114c3a8931))
* record Actions artifact cleanup ([#62](https://github.com/ildarbinanas-design/env-vault/issues/62)) ([b8bda39](https://github.com/ildarbinanas-design/env-vault/commit/b8bda393a634924f4b84a5d8c98a27051880310c))
* **release:** record v0.0.18 and plan artifact cleanup ([#56](https://github.com/ildarbinanas-design/env-vault/issues/56)) ([398b084](https://github.com/ildarbinanas-design/env-vault/commit/398b084b2f227ef5323f34b6dda15db8cd5d1256))

## [0.0.18](https://github.com/ildarbinanas-design/env-vault/compare/v0.0.17...v0.0.18) (2026-07-19)


### Documentation

* add release operations journal ([#54](https://github.com/ildarbinanas-design/env-vault/issues/54)) ([c2658bc](https://github.com/ildarbinanas-design/env-vault/commit/c2658bc9ba5a6d83578b04bebb6ae22b9505daea))

## [0.0.17](https://github.com/ildarbinanas-design/env-vault/compare/v0.0.16...v0.0.17) (2026-07-19)


### Bug Fixes

* **release:** recover empty release assets ([#49](https://github.com/ildarbinanas-design/env-vault/issues/49)) ([6989b73](https://github.com/ildarbinanas-design/env-vault/commit/6989b737c0e0a7407b5b7949840b0e139f406f16))
* **release:** recover Homebrew publication safely ([#52](https://github.com/ildarbinanas-design/env-vault/issues/52)) ([ce1ba71](https://github.com/ildarbinanas-design/env-vault/commit/ce1ba7186a4d3133fb04075f275f06e6042c0ccb))
* **release:** require GET for workflow queries ([#51](https://github.com/ildarbinanas-design/env-vault/issues/51)) ([2f92b66](https://github.com/ildarbinanas-design/env-vault/commit/2f92b66ee02eb77bb7bc6e628c4c2766c889ff20))


### Refactoring

* **release:** centralize operational contract ([#53](https://github.com/ildarbinanas-design/env-vault/issues/53)) ([0d87427](https://github.com/ildarbinanas-design/env-vault/commit/0d874277aad3bbfa21b12296d61df8a7f770d622))

## [0.0.16](https://github.com/ildarbinanas-design/env-vault/compare/v0.0.15...v0.0.16) (2026-07-17)


### Bug Fixes

* **release:** centralize GitHub transport ([#47](https://github.com/ildarbinanas-design/env-vault/issues/47)) ([2c6932f](https://github.com/ildarbinanas-design/env-vault/commit/2c6932fe10a70b3a7cf22e751965af8aca52cb6b))
* **release:** compact and anchor durable evidence ([#48](https://github.com/ildarbinanas-design/env-vault/issues/48)) ([fa5e3fd](https://github.com/ildarbinanas-design/env-vault/commit/fa5e3fdfe75c956dbd9e4f70484de1f0ec81de3a))


### Documentation

* freeze release refactor baseline ([#45](https://github.com/ildarbinanas-design/env-vault/issues/45)) ([114ab3e](https://github.com/ildarbinanas-design/env-vault/commit/114ab3e35b6948c0d40e5b7f9c97b867f32cf5eb))

## [0.0.15](https://github.com/ildarbinanas-design/env-vault/compare/v0.0.14...v0.0.15) (2026-07-17)


### Bug Fixes

* **release:** harden durable evidence identity ([#41](https://github.com/ildarbinanas-design/env-vault/issues/41)) ([8bf408a](https://github.com/ildarbinanas-design/env-vault/commit/8bf408acf804d8ef16644d9015928b3b1872e75b))
* **release:** require evidence branch bootstrap ([#44](https://github.com/ildarbinanas-design/env-vault/issues/44)) ([95a6293](https://github.com/ildarbinanas-design/env-vault/commit/95a62938edfa6206d1f1bcc34d0fefdbdff1bd5e))
* **release:** verify wrapped evidence blobs ([#43](https://github.com/ildarbinanas-design/env-vault/issues/43)) ([dc2bb7e](https://github.com/ildarbinanas-design/env-vault/commit/dc2bb7e989179698655e6dd139e3b0f94277d314))

## [0.0.14](https://github.com/ildarbinanas-design/env-vault/compare/v0.0.13...v0.0.14) (2026-07-17)


### Bug Fixes

* **release:** complete recovery and document operations ([#38](https://github.com/ildarbinanas-design/env-vault/issues/38)) ([c92467f](https://github.com/ildarbinanas-design/env-vault/commit/c92467faecf165795ab842dddc372ad97b183a8c))
* **release:** parse Homebrew health state exactly ([#37](https://github.com/ildarbinanas-design/env-vault/issues/37)) ([d4c8ac7](https://github.com/ildarbinanas-design/env-vault/commit/d4c8ac7c7364e8bc7f85a40bc8fb162bb506a48f))


### Tests

* **release:** accept forward manifest versions ([#40](https://github.com/ildarbinanas-design/env-vault/issues/40)) ([accd9fa](https://github.com/ildarbinanas-design/env-vault/commit/accd9fa79aa1378f012f83962e07d11ab20597c5))

## [0.0.13](https://github.com/ildarbinanas-design/env-vault/compare/v0.0.12...v0.0.13) (2026-07-17)


### Bug Fixes

* **ci:** make E2E reporter bootstrap deterministic ([#36](https://github.com/ildarbinanas-design/env-vault/issues/36)) ([50f927c](https://github.com/ildarbinanas-design/env-vault/commit/50f927ccc8a0ba6f7921f8fc5f27b31a0113141c))
* **release:** record authorization before merge ([#32](https://github.com/ildarbinanas-design/env-vault/issues/32)) ([0550a2b](https://github.com/ildarbinanas-design/env-vault/commit/0550a2b14c4d17e26e3d5a017c9118e018f3837c))
* **release:** recover abandoned v0.0.12 proposal ([#34](https://github.com/ildarbinanas-design/env-vault/issues/34)) ([5e3b94e](https://github.com/ildarbinanas-design/env-vault/commit/5e3b94ecd6f5b41f3f63bb2c878c7e65844da282))
* **release:** retry read-only GitHub observations ([#33](https://github.com/ildarbinanas-design/env-vault/issues/33)) ([e55cfdb](https://github.com/ildarbinanas-design/env-vault/commit/e55cfdbc3ba68e29eb68f0d438bd55cd3a93b394))

## [0.0.12](https://github.com/ildarbinanas-design/env-vault/compare/v0.0.11...v0.0.12) (2026-07-16)


### Bug Fixes

* **release:** align checksum line ending contract ([#30](https://github.com/ildarbinanas-design/env-vault/issues/30)) ([c7d3cd8](https://github.com/ildarbinanas-design/env-vault/commit/c7d3cd80dcdca69824400649431b791e3a0c9271))

## [0.0.11](https://github.com/ildarbinanas-design/env-vault/compare/v0.0.10...v0.0.11) (2026-07-16)


### Bug Fixes

* **release:** isolate publisher asset inventory ([#27](https://github.com/ildarbinanas-design/env-vault/issues/27)) ([5a37ea6](https://github.com/ildarbinanas-design/env-vault/commit/5a37ea653b40b583c4fee2153dfcf872216d0dfd))
* **release:** share README version contract ([#29](https://github.com/ildarbinanas-design/env-vault/issues/29)) ([f287f20](https://github.com/ildarbinanas-design/env-vault/commit/f287f2078ff59b18c0933e28a999a49dfb9f1fd8))

## [0.0.10](https://github.com/ildarbinanas-design/env-vault/compare/v0.0.9...v0.0.10) (2026-07-16)


### Bug Fixes

* **release:** locate nested promotion manifests ([#25](https://github.com/ildarbinanas-design/env-vault/issues/25)) ([c9ac7cc](https://github.com/ildarbinanas-design/env-vault/commit/c9ac7cced671e537441370e0b33da46448eb1de8))

## [0.0.9](https://github.com/ildarbinanas-design/env-vault/compare/v0.0.8...v0.0.9) (2026-07-16)


### Bug Fixes

* **e2e:** seal release-version contract normalization ([#24](https://github.com/ildarbinanas-design/env-vault/issues/24)) ([e298cd0](https://github.com/ildarbinanas-design/env-vault/commit/e298cd0d473c41654fb6464f16f11ee477e60547))
* **release:** add deterministic release diagnostics ([#20](https://github.com/ildarbinanas-design/env-vault/issues/20)) ([0eef845](https://github.com/ildarbinanas-design/env-vault/commit/0eef84548b802a89f98c08744b5b51aac3d543ad))
* **release:** promote exact CI artifacts ([#22](https://github.com/ildarbinanas-design/env-vault/issues/22)) ([d0c6a32](https://github.com/ildarbinanas-design/env-vault/commit/d0c6a320bade9d09b77ecfcca89d23daded57da6))
* **release:** verify read-only bypass state ([#23](https://github.com/ildarbinanas-design/env-vault/issues/23)) ([0b21e66](https://github.com/ildarbinanas-design/env-vault/commit/0b21e66664137a2944049633078a9658b4703625))

## [0.0.8](https://github.com/ildarbinanas-design/env-vault/compare/v0.0.7...v0.0.8) (2026-07-16)


### Bug Fixes

* **config:** stabilize Windows saves and automate releases ([#17](https://github.com/ildarbinanas-design/env-vault/issues/17)) ([8dfffca](https://github.com/ildarbinanas-design/env-vault/commit/8dfffca1b30db1a09c495b29064d599e9a3d556e))
* **release:** preserve read-only App audit ([#19](https://github.com/ildarbinanas-design/env-vault/issues/19)) ([33ae082](https://github.com/ildarbinanas-design/env-vault/commit/33ae082bc78d24184624de6cf88fb224e93182f4))


### Build System

* migrate env-vault to Go 1.26.5 ([#16](https://github.com/ildarbinanas-design/env-vault/issues/16)) ([a4e9a51](https://github.com/ildarbinanas-design/env-vault/commit/a4e9a5169959666a50f5022194d0b802cf3edac8))


### Tests

* **e2e:** establish Go 1.22 binary baseline ([#15](https://github.com/ildarbinanas-design/env-vault/issues/15)) ([7a044bd](https://github.com/ildarbinanas-design/env-vault/commit/7a044bdbf73aa592016bbb3a02d81f314f08fe63))

## [0.0.7](https://github.com/ildarbinanas-design/env-vault/compare/v0.0.6...v0.0.7) (2026-07-15)

### What's Changed

* Record healthy v0.0.6 release evidence by @ildarbinanas-design in https://github.com/ildarbinanas-design/env-vault/pull/11
* Make v0.0.6 evidence durable by @ildarbinanas-design in https://github.com/ildarbinanas-design/env-vault/pull/12
* fix(security): harden runtime and Homebrew release contract by @ildarbinanas-design in https://github.com/ildarbinanas-design/env-vault/pull/13

**Full Changelog**: https://github.com/ildarbinanas-design/env-vault/compare/v0.0.6...v0.0.7
