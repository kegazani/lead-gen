# C++ baseline feasibility check — macOS / zsh

Host: macOS 26.5.2 (build 25F84), arm64 (darwin25.5.0)
Workspace: `/Users/kgz/Desktop/p/lead-gen` — scratch only in `tmp/cpp-baseline-check/`
No `timeout`/`gtimeout` on this box → portable wrapper `tmo.sh` used for all caps.

## 1. Toolchain install (reversible)

| check | result | command |
|---|---|---|
| brew version | Homebrew **7.0.3** (assignment said 7.0.0) | `brew --version` |
| install cmake+ninja | OK, ~18 s | `tmo.sh 420 brew install cmake ninja` |
| cmake | **4.4.3** (`/opt/homebrew/bin/cmake`) | `cmake --version` |
| ninja | **1.13.2** (`/opt/homebrew/bin/ninja`) | `ninja --version` |
| pkg-config | **absent** | `which pkg-config` → not found |
| revert | `brew uninstall cmake ninja` | — |

Compilers: `/usr/bin/clang++` = Apple clang 21.0.0 (clang-2100.1.1.101).
Default language level is **C++14** (`__cplusplus == 201402L`) → `-std=c++20` is mandatory.

## 2. C++20

| check | result | command |
|---|---|---|
| `<coroutine>` (promise_type + custom awaitable + co_await/co_return) | **PASS** `coroutine result=45 expected=45 -> OK`, rc=0, 0 warnings | `clang++ -std=c++20 -Wall -Wextra -O2 coro_test.cpp -o coro_test && ./coro_test` |
| `<format>` | **PASS** `hello world 0007 3.14`, rc=0, 0 warnings | `clang++ -std=c++20 -Wall -Wextra -O2 format_test.cpp -o format_test && ./format_test` |
| cmake+ninja end-to-end (`CMAKE_CXX_STANDARD 20`) | **PASS**, generator `Ninja`, both binaries build and run | `cmake -S . -B build -G Ninja && cmake --build build` |

## 3. Formula availability — `brew info --json=v2` (13/13 present, nothing installed)

| formula | present | version | license | bottle |
|---|---|---|---|---|
| drogon | yes | 1.9.13 | MIT | arm64_tahoe |
| libpqxx | yes | 8.0.2 | BSD-3-Clause | arm64_tahoe |
| libzip | yes | 1.11.4 | BSD-3-Clause | arm64_tahoe |
| minizip-ng | yes | 4.2.2 | Zlib | arm64_tahoe |
| libxlsxwriter | yes | 1.2.4 | BSD-2-Clause | arm64_tahoe |
| googletest | yes | 1.18.0 | BSD-3-Clause | arm64_tahoe |
| opentelemetry-cpp | yes | 1.29.0 | Apache-2.0 | arm64_tahoe |
| aws-sdk-cpp | yes | 1.11.885 | Apache-2.0 | arm64_tahoe |
| rabbitmq-c | yes | 0.18.0 | MIT | arm64_tahoe |
| hiredis | yes | 1.4.1 | BSD-3-Clause | arm64_tahoe |
| nlohmann-json | yes | 3.12.0 | MIT | `all` (header-only) |
| inja | yes | 3.5.0 | MIT | `all` (header-only) |
| tdlib | yes | 1.8.0 | BSL-1.0 | arm64_tahoe |

None deprecated/disabled; none `keg_only`. Raw JSON kept in `finfo/`.
**No formula installation was run.**

## 4. Network

| target | result | command |
|---|---|---|
| https://github.com | `HTTP/2 200` | `tmo.sh 8 curl -sSI --max-time 8 <url>` |
| https://raw.githubusercontent.com | `HTTP/2 301` | `tmo.sh 8 curl -sSI --max-time 8 <url>` |

## 5. Render determinism

Browser: **Google Chrome 153.0.8010.48**

| run | command | raw sha256 |
|---|---|---|
| out1/out2 (default header) | `... --headless --disable-gpu --print-to-pdf=outN.pdf file://render.html` | A = `78c39416…` / `4ec2f48b…` |
| out3/out4/out5 | same | B = `fea47411…` / `22f75a80…`(norm) |

Verdict: **raw SHA-256 is NOT reproducible out of the box.** Only two variants exist across 5 renders.

Root cause (byte-level, not guesswork):
- `/CreationDate` + `/ModDate` differ on every render (6 bytes) — always.
- Variant A vs B differ by **exactly 2 glyph ids** inside
  `/Artifact <</Type /Pagination /Subtype /Header>>` → Chrome's default print header.
  Decoded page text diff is one line: `9/17/26, 11:09 AM` → `9/17/26, 11:10 AM` (minute resolution).
- All 9 content/font streams otherwise byte-identical: embedded font programs (objs 36/40/44/48) and
  ToUnicode CMaps (39/43/47/51) hash the same; body text identical.

Control run with `--no-pdf-header-footer`: 3 renders, all 33492 B, raw hashes differ,
but after normalizing **only** `/CreationDate`/`/ModDate` all three are byte-identical:
`9894376691016e6f7a945fa8c5cf1c2f35db36097b483d014486133a2c9694e0`.

Conclusion: rendering is deterministic; the PDF *file* is not hashable without date normalization.
For golden-file tests either normalize the two date fields or set `SOURCE_DATE_EPOCH`-style post-processing.

## 6. Command caps used

`tmo.sh <sec>` on every command: 8 s (curl), 60 s (brew info), 110 s (Chrome), 120 s (compiles/cmake).
Deviation: the one `brew install` was capped at 420 s instead of ~120 s (legitimately long op); it finished in 18 s.
