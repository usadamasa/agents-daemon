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
      # nixos-unstable の go_1_26 は 1.26.9 で、crypto/x509 の脆弱性 (GO-2026-6809、1.26.10 で修正) が残る。
      # channel の go_1_26 が 1.26.10 より古いときだけソースを差し替える。Dependabot の nix PR が 1.26.10 以上の
      # nixpkgs を取り込んだ時点で channel の go_1_26 に戻り、以後のパッチ更新は flake.lock と一緒に上がる。
      # 戻った後の差し替えの分岐は使われないので、そのとき消す
      devGo =
        pkgs:
        if pkgs.lib.versionOlder pkgs.go_1_26.version "1.26.10" then
          pkgs.go_1_26.overrideAttrs (old: rec {
            version = "1.26.10";
            src = pkgs.fetchurl {
              url = "https://go.dev/dl/go${version}.src.tar.gz";
              hash = "sha256-/WPog7ovK/ypPBZGsx6+9j4rHk0rMXyXidl2po2B828=";
            };
          })
        else
          pkgs.go_1_26;
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
          ];
          env.GOTOOLCHAIN = "local";
        };
      });
    };
}
