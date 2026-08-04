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
