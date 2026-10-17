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

      # nixpkgs に無いツール。analyze-* は go.mod の tool を Taskfile が go tool で動かすので含めない
      extraTools =
        pkgs:
        let
          go = pkgs.go_1_26;
        in
        {
          spm-go = pkgs.buildGoModule.override { inherit go; } rec {
            pname = "spm-go";
            version = "0.11.1";
            src = pkgs.fetchFromGitHub {
              owner = "fdaines";
              repo = "spm-go";
              rev = "v${version}";
              hash = "sha256-lg6UX8N7Rys4wEUEdKmNoLzrURcqjS/9eyIKCMNFW3A=";
            };
            vendorHash = "sha256-EuHp0OPyVln/ytK9Vav/5U7EsUc80g3LVWGMqdpY3dg=";
            # -trimpath でビルドすると GOROOT が埋め込まれず、spm-go は go list を見つけられないまま
            # パッケージ 0 件の表を exit 0 で出す。go install 版と同じく、ビルドした Go の GOROOT を既定にする
            allowGoReference = true;
            nativeBuildInputs = [ pkgs.makeWrapper ];
            postFixup = ''
              wrapProgram $out/bin/spm-go --set-default GOROOT ${go}/share/go
            '';
          };
        };
    in
    {
      packages = forAllSystems extraTools;

      devShells = forAllSystems (
        pkgs:
        let
          tools = extraTools pkgs;
        in
        {
          default = pkgs.mkShellNoCC {
            packages = with pkgs; [
              # go.mod の go 行と同じ版。ずれたら GOTOOLCHAIN=local で即座に失敗させる。spm-go の Go と揃える
              go_1_26
              go-task
              golangci-lint
              go-arch-lint
              gosec
              govulncheck
              bats
              shellcheck
              # hook と bats のテストが呼ぶ
              jq
              tools.spm-go
            ];
            env.GOTOOLCHAIN = "local";
          };
        }
      );
    };
}
