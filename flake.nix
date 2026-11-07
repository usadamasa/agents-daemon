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
    in
    {
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShellNoCC {
          # analyze-* と vulncheck は go.mod の tool を Taskfile が go tool で動かすので含めない。
          # go-arch-lint は analyze-arch-lint が PATH から呼ぶので tool ではなくここに置く
          packages = with pkgs; [
            # go.mod の go 行と同じ系列。ずれたら GOTOOLCHAIN=local で即座に失敗させる
            go_1_26
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
