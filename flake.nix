{
  description = "airgap - unified offline sync tool for disconnected environments";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-24.11";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        # Development shell — `nix develop`
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            # Go toolchain
            go_1_23
            gopls
            gotools        # goimports, gorename, etc.
            go-tools       # staticcheck
            delve          # debugger
            golangci-lint  # linter aggregator

            # Build tools
            gnumake
            git

            # Container tooling
            podman
            skopeo

            # Compression (for export engine)
            zstd

            # General utilities
            jq
            yq-go
            curl
            sqlite
          ] ++ pkgs.lib.optionals pkgs.stdenv.isLinux [
            # RPM repo tools (Linux only — doesn't build on macOS)
            createrepo_c
          ];

          shellHook = ''
            echo "airgap dev shell — Go $(go version | cut -d' ' -f3)"
            echo ""
            echo "Quick start:"
            echo "  go mod tidy        — resolve dependencies"
            echo "  go build ./...     — build all packages"
            echo "  go test ./...      — run all tests"
            echo "  go test -v ./...   — run tests verbose"
            echo "  make build         — build binary"
            echo "  make test          — run tests"
            echo "  make lint          — run linters"
            echo ""
            export GOPATH="$HOME/go"
            export PATH="$GOPATH/bin:$PATH"
          '';
        };

        # Build the airgap binary — `nix build`
        packages.default = pkgs.buildGoModule {
          pname = "airgap";
          version = "0.1.0-dev";
          src = ./.;

          # After first successful build, replace this with the real hash:
          #   nix build 2>&1 | grep "got:" | awk '{print $2}'
          vendorHash = null; # using go modules, not vendored

          # Only build the main binary
          subPackages = [ "cmd/airgap" ];

          meta = with pkgs.lib; {
            description = "Unified offline sync tool for disconnected OCP environments";
            homepage = "https://github.com/BadgerOps/airgap";
            license = licenses.agpl3Only;
            maintainers = [];
            mainProgram = "airgap";
          };
        };

        # Container image — `nix build .#container`
        packages.container = pkgs.dockerTools.buildLayeredImage {
          name = "airgap";
          tag = "latest";
          contents = [
            self.packages.${system}.default
            pkgs.cacert        # TLS certificates
            pkgs.zstd          # compression
            pkgs.createrepo_c  # RPM repo metadata
            pkgs.sqlite        # DB inspection
            pkgs.coreutils
            pkgs.bash
          ];
          config = {
            Cmd = [ "/bin/airgap" "serve" ];
            ExposedPorts = { "8080/tcp" = {}; };
            Volumes = {
              "/var/lib/airgap" = {};
              "/mnt/transfer-disk" = {};
            };
            Env = [
              "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
            ];
          };
        };
      }
    );
}
