{
  description = "sys-bozo — workstation control-center TUI";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.05";

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
            vendorHash = "sha256-hzF4U/qjdwh8L4I90P4x3GGtwZzD2lvmMe3HLIDETx4=";
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
