{
  description = "ISO - Isolated Docker Environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "iso";
          version = "0.1.0";
          src = ./.;
          vendorHash = "sha256-5IBN44wt6A7tguKxWeo5cvXWCaiSpgg/QZCv9yMZkRE=";
          subPackages = [ "cmd/iso" ];
          tags = [ "linux_build" ];
        };

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            golangci-lint
          ];
        };
      }
    );
}
