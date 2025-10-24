{
  description = "ISO - Isolated Docker Environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = nixpkgs.legacyPackages.${system};

        # Build portable Linux binaries for embedding in containers
        buildEmbeddedBinary =
          targetArch:
          pkgs.buildGoModule {
            pname = "iso-${targetArch}";
            version = "0.1.0";
            src = ./.;
            vendorHash = "sha256-7DURQKtPSqVcIQ+ksAkp/ZgQm/1imCLyXDmq8PqR+PY=";
            subPackages = [ "cmd/iso" ];
            tags = [ "linux_build" ];

            ldflags = [
              "-s"
              "-w"
            ];

            preBuild = ''
              export CGO_ENABLED=0
            '';

            installPhase = ''
              mkdir -p $out
              cp $GOPATH/bin/iso $out/iso-linux-${targetArch}
            '';
          };

        iso-amd64 = buildEmbeddedBinary "amd64";
        iso-arm64 = buildEmbeddedBinary "arm64";
      in
      {
        packages.default = pkgs.buildGoModule {
          pname = "iso";
          version = "0.1.0";
          src = ./.;
          vendorHash = "sha256-7DURQKtPSqVcIQ+ksAkp/ZgQm/1imCLyXDmq8PqR+PY=";
          subPackages = [ "cmd/iso" ];

          ldflags = [
            "-s"
            "-w"
          ];

          preBuild = ''
            export CGO_ENABLED=0
            mkdir -p build
            gzip -c ${iso-amd64}/iso-linux-amd64 > build/iso-linux-amd64.gz
            gzip -c ${iso-arm64}/iso-linux-arm64 > build/iso-linux-arm64.gz
          '';
        };

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            golangci-lint
          ];
        };

        formatter = pkgs.nixfmt-rfc-style;
      }
    );
}
