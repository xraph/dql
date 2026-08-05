## [1.3.1](https://github.com/xraph/dql/compare/v1.3.0...v1.3.1) (2026-08-05)

### Documentation

* *sql.DB does not satisfy SQLQuerier on its own ([4cd0941](https://github.com/xraph/dql/commit/4cd09418bb86c533e3e67262d2d86df3d0564ce6))

## [1.3.0](https://github.com/xraph/dql/compare/v1.2.1...v1.3.0) (2026-08-04)

### Features

* **exec:** serve the pushed prefix as a cursor when nothing needs post-processing ([47b05e2](https://github.com/xraph/dql/commit/47b05e2852441963f3ce97dede8f00808213b90e))
* **pipe:** add the sheet operator ([6407644](https://github.com/xraph/dql/commit/640764491fd3f927852042406df690d31e75faed))
* **pipe:** pin sheet and window composition, and drop the planned window kind ([950c57b](https://github.com/xraph/dql/commit/950c57b207ba84df05acf04b0637119b5bc78403))
* **sheet,pipe:** delegate eligible reduces to the source when the prefix was complete ([bf059dc](https://github.com/xraph/dql/commit/bf059dcb6ec84df23958a90b4b437ed0baa66b99))
* **sheet,pipe:** draw the prefix through a cursor so completeness is knowable ([67b490a](https://github.com/xraph/dql/commit/67b490ae93195030d43b7c8c96d026f8ded78a98))
* **sheet:** bound the column cache, spilling the least recently used ([b8040ba](https://github.com/xraph/dql/commit/b8040ba2ef62bb817dbfe2370d9851a1d7f7ef98))
* **sheet:** let a host register its own reduce kernels, per OpContext ([c8d057a](https://github.com/xraph/dql/commit/c8d057a0429dbe7db317df6062d112bf0dc71003))
* **sheet:** the engine — compile once, order by dependency, reduce natively ([4c929b8](https://github.com/xraph/dql/commit/4c929b8dda63d687ae0b77735ab4056d6511119c))
* **sheet:** typed columns with a separate null bitmap, and a builder that narrows then demotes ([7111dc3](https://github.com/xraph/dql/commit/7111dc3ff32670578d36c40ea5342966106fcec4))

### Performance Improvements

* **sheet:** allocate columns once and reuse them across reduces ([7a9ded0](https://github.com/xraph/dql/commit/7a9ded075188a664f0afccb1a371442d38f8e6c0))

### Documentation

* plan the core of sheet semantics ([e62bb65](https://github.com/xraph/dql/commit/e62bb656b13b842a2c1b207a9157fdf498a04404))
* pushdown depends on streaming, and record what the benchmarks showed ([fcd84af](https://github.com/xraph/dql/commit/fcd84af29799481384bd2eb2490a40d50554f6e4))

## [1.2.1](https://github.com/xraph/dql/compare/v1.2.0...v1.2.1) (2026-08-04)

### Documentation

* note that the site checks the operator page itself ([0b7bf92](https://github.com/xraph/dql/commit/0b7bf922ad6bd7349f7f67422037dcc85971f1e6))
* require an expression compiler for sheets rather than deriving references in DQL ([656fb22](https://github.com/xraph/dql/commit/656fb228c7663ecef12ee84e326d243e33e8a5e3))

## [1.2.0](https://github.com/xraph/dql/compare/v1.1.3...v1.2.0) (2026-08-04)

### Features

* **pipe:** render the operator reference as MDX for the docs site ([e2f43fa](https://github.com/xraph/dql/commit/e2f43fa5812bd2870a859057787fce885bdfcd16))

### Documentation

* design sheet semantics as a first-class operator ([fa9a6fa](https://github.com/xraph/dql/commit/fa9a6fa623dbd4e3bc4725e47c30eec6885545b4))

## [1.1.3](https://github.com/xraph/dql/compare/v1.1.2...v1.1.3) (2026-08-04)

### Performance Improvements

* **pipe:** stop rebuilding the merge plan for every joined row ([b7e03e0](https://github.com/xraph/dql/commit/b7e03e04851fec957a0772c46b85068d5fd8942e))

### Documentation

* **bench:** record the join measurements from an idle machine ([8945ae7](https://github.com/xraph/dql/commit/8945ae73e085c48eae00d9c2fe3aedddc7fbd3de))

## [1.1.2](https://github.com/xraph/dql/compare/v1.1.1...v1.1.2) (2026-08-04)

### Documentation

* fix the pipe example, which named an operator that does not exist ([c6ad07a](https://github.com/xraph/dql/commit/c6ad07a74211de7f7d70dea9f0d5787fdc526dfc))

## [1.1.1](https://github.com/xraph/dql/compare/v1.1.0...v1.1.1) (2026-08-04)

### Documentation

* **bench:** record the post-optimisation baseline ([a60ada4](https://github.com/xraph/dql/commit/a60ada489a778a640f95c9cad44119e8d8ad3df8))

## [1.1.0](https://github.com/xraph/dql/compare/v1.0.0...v1.1.0) (2026-08-04)

### Features

* **pipe:** declare operator host requirements and generate the reference ([2093860](https://github.com/xraph/dql/commit/2093860e6c3db73f141a0b56dd2d76105408508e))

### Bug Fixes

* **ci:** stop CRLF checkouts failing the reference test on Windows ([2ee399f](https://github.com/xraph/dql/commit/2ee399fbd4b13535e6212743d733b61dff918397))

### Performance Improvements

* **pipe:** extract sort keys once in sortOp too ([caad64a](https://github.com/xraph/dql/commit/caad64a11561bed966163e093063f3c39f428100))
* **pipe:** extract sort keys once instead of per comparison ([f7aa834](https://github.com/xraph/dql/commit/f7aa8347ce8b788a7512a3d270e829cdf0804fda))

### Documentation

* **bench:** design the benchmark suite ([cf5bbf8](https://github.com/xraph/dql/commit/cf5bbf8c0006f0bda4c11cf0d7e9a556eafbb5fa))
* **bench:** record baseline results and the window scaling finding ([504ae3f](https://github.com/xraph/dql/commit/504ae3f8650dc6e593c1ad5fe48e5af05e232e2a))
* **bench:** record the applied comparator fix and its measurements ([8704b2d](https://github.com/xraph/dql/commit/8704b2df8464ab0b431f8a7ff8e97ee3c017d90b))

## 1.0.0 (2026-08-03)

### Features

* DQL, a declarative query language for Go ([14940c7](https://github.com/xraph/dql/commit/14940c7d63a1ad4512f9740022f6881a00593991))
* editor intelligence as a lang package and an LSP server ([c820fe5](https://github.com/xraph/dql/commit/c820fe5ab42f5118f35ed335fc420465a6c49a4e))
* **syntaxes:** register cleanly with Shiki ([2943678](https://github.com/xraph/dql/commit/2943678b1f9b6326be0df141686faf81f0dd6bd4))
* **syntaxes:** ship the TextMate grammar with the language ([b25cee8](https://github.com/xraph/dql/commit/b25cee85d2fc5c39d24f38eb889955c6d0217d9e))

### Bug Fixes

* **ci:** depend on the published adapter, annotate a scanner false positive ([225948e](https://github.com/xraph/dql/commit/225948e186b5326f3a5d921084b5e39a5c9f2cf1))

### Documentation

* DQL is Data Query Language ([74d19f7](https://github.com/xraph/dql/commit/74d19f7d4ed4c75638c5800078da07d7ca50164a))
* say the name is not an acronym ([ce6ac03](https://github.com/xraph/dql/commit/ce6ac03532726b25765ec75eb5c90ba256ad581c))
