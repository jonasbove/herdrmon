{
  description = "herdrmon: a FireRed party-menu device picker for Herdr, with a Gen 3 icon font and a Herdr sidebar plugin";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        lib = pkgs.lib;

        # Pinned pret sources. The art itself never enters this repo or the
        # Nix store output of packages.herdrmon-icons, which contains only
        # the compiled font and frames.json derived from it.
        pokeemerald-src = pkgs.fetchFromGitHub {
          owner = "pret";
          repo = "pokeemerald";
          rev = "fe1d8e51b265851676b65885818b1f5f62586135";
          sha256 = "1c74ldglxl9841vyph0qg1vwxmvdg8kwp8366n8ic9mshjlwx1gk";
        };
        pokefirered-src = pkgs.fetchFromGitHub {
          owner = "pret";
          repo = "pokefirered";
          rev = "037335f4c725d7c9aecdac87066f2002b4bd7e14";
          sha256 = "1ccvkahb4yjaxbmy0x4yrf5yc8bvhv3if6dydh51bzr24qc0260p";
        };

        defaultMons = [ "geodude" "onix" "porygon" "magnemite" "voltorb" "beldum" ];

        herdrmon = pkgs.buildGoModule {
          pname = "herdrmon";
          version = "0.1.0";
          src = lib.cleanSourceWith {
            src = ./.;
            filter = path: type:
              let base = baseNameOf path;
              in !(lib.hasSuffix ".nix" base || base == "flake.lock" || base == "font" || base == "omarchy"
                   || base == "sidebar" || base == "docs" || base == "testdata");
          };
          # Computed with `nix build .#herdrmon` against go.mod/go.sum.
          vendorHash = "sha256-savm3BtSeEdabw47mhPKLDXwfz+y7WayJWc+AaGZVq8=";

          # buildGoModule's default checkPhase runs `go test ./...`, and
          # TestServeAndHTTP binds a real 127.0.0.1 HTTP server to probe —
          # unreliable inside the Nix build sandbox's network namespace. Test
          # correctness belongs to `go test .` in CI/dev (see README), not
          # to this packaging build.
          doCheck = false;

          meta = with lib; {
            description = "FireRed-style party menu for picking a Herdr device on your tailnet";
            homepage = "https://github.com/jonasbove/herdrmon";
            license = licenses.mit;
            mainProgram = "herdrmon";
          };
        };

        # The font + frames.json, built from pinned pret sources at eval
        # time. The art itself never enters $out, only what build.py derives
        # from it (the compiled font + frames.json). --emerald/--firered
        # point build.py's fetch step at the pinned local checkouts above
        # instead of the network; --no-guard skips the fc-list collision
        # check, which only matters against fonts already installed on a
        # real machine, not in the sandbox. An explicit empty --config
        # sidesteps build.py falling back to $XDG_CONFIG_HOME/herdrmon
        # /config.toml (harmless either way in the sandbox, but explicit is
        # clearer than relying on that fallback being absent).
        mkHerdrmonIcons = { mons ? defaultMons }:
          pkgs.stdenvNoCC.mkDerivation {
            pname = "herdrmon-icons";
            version = "0.1.0";
            src = lib.cleanSourceWith {
              src = ./font;
              filter = path: type: baseNameOf path != "assets" && baseNameOf path != "out";
            };
            nativeBuildInputs = [
              (pkgs.python3.withPackages (ps: [ ps.fonttools ps.pillow ps.skia-pathops ]))
            ];
            dontConfigure = true;
            buildPhase = ''
              runHook preBuild
              : > empty-config.toml
              mkdir -p "$out/share/fonts" "$out/share/herdrmon"
              python3 build.py \
                --config empty-config.toml \
                --assets "$TMPDIR/assets" \
                --mons "${lib.concatStringsSep "," mons}" \
                --emerald "file://${pokeemerald-src}" \
                --firered "file://${pokefirered-src}" \
                --no-guard \
                --out-font "$out/share/fonts/HerdrmonIcons.otf" \
                --out-frames "$out/share/herdrmon/frames.json"
              runHook postBuild
            '';
            dontInstall = true;
            passthru = {
              inherit mons;
              # `.override { mons = [ ... ]; }` per DESIGN.md.
              override = { mons }: mkHerdrmonIcons { inherit mons; };
            };
            meta.description = "Herdrmon Gen 3 icon font + frames.json, baked from pinned pret sources";
          };
        herdrmon-icons = mkHerdrmonIcons { };

        # A pure-Node Herdr plugin with no npm dependencies and no build step
        # (sidebar/herdr-plugin.toml, sidebar/README.md) — just copy it in.
        herdrmon-sidebar = pkgs.stdenvNoCC.mkDerivation {
          pname = "herdrmon-sidebar";
          version = "0.1.0";
          src = ./sidebar;
          dontBuild = true;
          installPhase = ''
            runHook preInstall
            mkdir -p "$out"
            cp -r . "$out/"
            runHook postInstall
          '';
          meta.description = "Herdr plugin: animates each machine's Pokemon in the Herdr sidebar";
        };

        tomlFormat = pkgs.formats.toml { };
      in
      {
        packages = {
          inherit herdrmon herdrmon-icons herdrmon-sidebar;
          default = herdrmon;
        };

        devShells.default = pkgs.mkShell {
          packages = [
            pkgs.go_1_27
            pkgs.uv
            (pkgs.python3.withPackages (ps: [ ps.fonttools ps.pillow ps.skia-pathops ]))
            pkgs.nodejs_22
            pkgs.gotools
            pkgs.gopls
          ];
        };

        formatter = pkgs.nixpkgs-fmt;

        checks.build = herdrmon;
      }) // {
      homeManagerModules.default = { config, lib, pkgs, ... }:
        let
          cfg = config.programs.herdrmon;
          pkg = self.packages.${pkgs.system}.herdrmon;
          iconsPkg = self.packages.${pkgs.system}.herdrmon-icons.override { mons = cfg.mons; };
          sidebarPkg = self.packages.${pkgs.system}.herdrmon-sidebar;
          tomlFormat = pkgs.formats.toml { };
        in
        {
          options.programs.herdrmon = {
            enable = lib.mkEnableOption "herdrmon, a FireRed party menu for picking a Herdr device";
            settings = lib.mkOption {
              type = tomlFormat.type;
              default = { };
              description = "herdrmon config.toml contents (see DESIGN.md's config.toml reference).";
            };
            mons = lib.mkOption {
              type = lib.types.listOf lib.types.str;
              default = [ ];
              description = "Extra mons to bake into the icon font beyond those implied by [[device]] entries.";
            };
            sidebar.enable = lib.mkEnableOption "the herdrmon Herdr sidebar plugin";
          };

          config = lib.mkIf cfg.enable {
            home.packages = [ pkg iconsPkg ] ++ lib.optional cfg.sidebar.enable sidebarPkg;
            xdg.configFile."herdrmon/config.toml".source =
              tomlFormat.generate "herdrmon-config.toml" cfg.settings;
          };
        };
    };
}
