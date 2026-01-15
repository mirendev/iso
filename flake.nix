{
  description = "ISO - Isolated Docker Environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.05";
    flake-utils.url = "github:numtide/flake-utils";
    quake = {
      url = "github:mirendev/quake";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-utils.follows = "flake-utils";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
      quake,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        # Build portable Linux binary for embedding in containers
        # Only build for the current system architecture
        embeddedBinary = pkgs.buildGoModule {
          pname = "iso-embedded";
          version = "0.1.0";
          src = self;
          vendorHash = "sha256-7DURQKtPSqVcIQ+ksAkp/ZgQm/1imCLyXDmq8PqR+PY=";
          subPackages = [ "cmd/iso" ];
          tags = [ "linux_build" ];

          env = {
            CGO_ENABLED = "0";
          };

          ldflags = [
            "-s"
            "-w"
            "-X main.commit=${self.rev or "dirty"}"
          ];


          installPhase = ''
            mkdir -p $out
            cp $GOPATH/bin/iso $out/iso-linux-$GOARCH
          '';
        };
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "iso";
          version = "0.1.0";
          src = self;
          vendorHash = "sha256-7DURQKtPSqVcIQ+ksAkp/ZgQm/1imCLyXDmq8PqR+PY=";
          subPackages = [ "cmd/iso" ];
          tags = [ "embed_binaries" ];

          env = {
            CGO_ENABLED = "0";
          };

          ldflags = [
            "-s"
            "-w"
            "-X main.commit=${self.rev or "dirty"}"
          ];

          preBuild = ''
            mkdir -p build
            # Copy and gzip the embedded binary for our architecture
            gzip -c ${embeddedBinary}/iso-linux-$GOARCH > build/iso-linux-$GOARCH.gz
            # Create empty placeholder for the other architecture (go:embed requires it to exist)
            if [ "$GOARCH" = "amd64" ]; then
              : | gzip > build/iso-linux-arm64.gz
            else
              : | gzip > build/iso-linux-amd64.gz
            fi
          '';
        };

        devShells.default = pkgs.mkShell {
          buildInputs = [
            pkgs.go
            pkgs.golangci-lint
            quake.packages.${system}.default
          ];
        };

        formatter = pkgs.nixfmt-rfc-style;
      }
    );
}
