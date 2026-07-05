{
  description = "sys-bozo — workstation control-center TUI";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-24.11-darwin";

  outputs = { self, nixpkgs }:
    let
      systems = [ "aarch64-darwin" "x86_64-linux" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f system);
    in {
      packages = forAllSystems (system:
        let pkgs = nixpkgs.legacyPackages.${system};
        in {
          default = pkgs.buildGoModule {
            pname = "sys-bozo";
            version = "dev";
            src = ./.;
            vendorHash = "sha256-cuDmOnJis3VuLi957hXCtP43KagxcsNCVX51YSdLxl0=";
            meta.mainProgram = "sys-bozo";
          };
        }
      );

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/sys-bozo";
        };
      });
    };
}
