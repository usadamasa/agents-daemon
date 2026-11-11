{
  description = "agents-daemon の開発環境 (Go と lint・テストのツール)";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { nixpkgs, ... }:
    let
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-linux"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      # nixos-unstable の go_1_27 は 1.27.3 で、1.27.4 で修正された脆弱性 (net/http の MaxBytesReader の
      # データ競合、cgo 経由のビルド時の任意コード実行) が残る。channel の go_1_27 が 1.27.4 より古いときだけ
      # go.dev の配布バイナリに差し替える。Dependabot の nix PR が 1.27.4 以上の nixpkgs を取り込んだ時点で
      # channel の go_1_27 に戻り、以後のパッチ更新は flake.lock と一緒に上がる。戻った後の差し替えの分岐は
      # 使われないので、そのとき消す
      devGo =
        pkgs:
        if pkgs.lib.versionOlder pkgs.go_1_27.version "1.27.4" then
          pkgs.stdenvNoCC.mkDerivation rec {
            pname = "go-bin";
            version = "1.27.4";
            src =
              let
                platform = with pkgs.stdenvNoCC.hostPlatform.go; "${GOOS}-${GOARCH}";
              in
              pkgs.fetchurl {
                url = "https://go.dev/dl/go${version}.${platform}.tar.gz";
                # https://go.dev/dl/?mode=json&include=all の sha256
                sha256 =
                  {
                    darwin-arm64 = "91a44a2b8a716600ad94a4f143e156116620db8828086a6e3602291be9bd0bb4";
                    linux-amd64 = "7226736c2339719cda28fbf3a54d71aa63bcefe376e98cef00034268dab24840";
                    linux-arm64 = "3392582988a273a87ab5043ee1b2c9c17a2c3c00580bf9326c47a2e0aaaeac92";
                  }
                  .${platform};
              };
            # strip は darwin の署名を壊す。配布物をそのまま置く
            dontFixup = true;
            installPhase = ''
              mkdir -p $out/share $out/bin
              cp -r . $out/share/go
              ln -s $out/share/go/bin/go $out/share/go/bin/gofmt $out/bin/
            '';
          }
        else
          pkgs.go_1_27;
    in
    {
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShellNoCC {
          # analyze-* と vulncheck は go.mod の tool を Taskfile が go tool で動かすので含めない。
          # go-arch-lint は analyze-arch-lint が PATH から呼ぶので tool ではなくここに置く
          packages = with pkgs; [
            # go.mod の go 行と同じ系列。ずれたら GOTOOLCHAIN=local で即座に失敗させる
            (devGo pkgs)
            go-task
            golangci-lint
            go-arch-lint
            gosec
            bats
            shellcheck
            # hook と bats のテストが呼ぶ
            jq
            # models/ の TLA+ のモデル検査 (tlc)。CI では回さない
            tlaplus
            # mod (hooks/*.ts、*.tsx) の型検査 (tsc)。nixpkgs の typescript は 7 系 (Go 実装、旧 typescript-go)。
            # 型は Claude Code が .claude-plugin/types/ に置く
            typescript
            # gui/frontend の npm (task gui:frontend)。配置の library を取るだけで bundler は使わない
            nodejs
          ];
          env.GOTOOLCHAIN = "local";
        };
      });
    };
}
