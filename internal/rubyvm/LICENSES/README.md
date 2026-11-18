# Embedded Ruby

`ruby.wasm.gz` is CRuby 3.4.1 compiled to WebAssembly (WASI) by the
[ruby.wasm](https://github.com/ruby/ruby.wasm) project, gzip-compressed and
otherwise unchanged.

- Source: https://github.com/ruby/ruby.wasm/releases/download/2.7.1/ruby-3.4-wasm32-unknown-wasip1-minimal.tar.gz
- sha256 of that archive: `e7c42626d655be95dd7dc87c52ff7a12445588fb7f40dbc6b1155b5c966387d6`
- sha256 of `usr/local/bin/ruby` inside it (= `ruby.wasm.gz` uncompressed): `e24b32b4c0a124761ab661852e3316646cfe98f831d30eb775aca0feb67d3213`

Ruby is distributed under the Ruby licence or the 2-clause BSD licence
(`ruby-COPYING`, `ruby-BSDL`). Some files inside Ruby have their own
notices; see https://github.com/ruby/ruby/blob/v3_4_1/LEGAL. ruby.wasm's build
tooling is MIT-licensed (`ruby.wasm-LICENSE`).

To refresh it: download the archive above (or a newer one), then
`gzip -9 -c usr/local/bin/ruby > internal/rubyvm/ruby.wasm.gz`.
