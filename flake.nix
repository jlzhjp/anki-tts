{
  description = "Generate and attach TTS audio to Anki notes";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs, ... }:
    let
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-darwin"
        "x86_64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          anki-tts = pkgs.buildGoModule {
            pname = "anki-tts";
            version = "0.1.0";

            src = ./.;
            vendorHash = "sha256-iW3EILAKfALa3Hv2bntGqbVRcRmiLJ2aFrV4bJhWz24=";

            nativeBuildInputs = [
              pkgs.installShellFiles
              pkgs.makeWrapper
            ];

            postInstall = ''
              wrapProgram $out/bin/anki-tts \
                --prefix PATH : ${nixpkgs.lib.makeBinPath [ pkgs.ffmpeg ]}
            ''
            + nixpkgs.lib.optionalString (pkgs.stdenv.buildPlatform.canExecute pkgs.stdenv.hostPlatform) ''
              installShellCompletion --cmd anki-tts \
                --bash <($out/bin/anki-tts completion bash) \
                --fish <($out/bin/anki-tts completion fish) \
                --zsh <($out/bin/anki-tts completion zsh)
            '';

            meta = {
              description = "Generate and attach TTS audio to Anki notes";
              mainProgram = "anki-tts";
              platforms = nixpkgs.lib.platforms.unix;
            };
          };
        in
        {
          inherit anki-tts;
          default = anki-tts;
        }
      );

      apps = forAllSystems (system: {
        anki-tts = {
          type = "app";
          program = nixpkgs.lib.getExe self.packages.${system}.anki-tts;
          meta.description = "Generate and attach TTS audio to Anki notes";
        };
        default = self.apps.${system}.anki-tts;
      });

      checks = forAllSystems (system: {
        inherit (self.packages.${system}) anki-tts;
      });

      devShells = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.mkShell {
            packages = with pkgs; [
              deadnix
              ffmpeg
              go
              gopls
              gotools
              nixd
              nixfmt
              statix
            ];
          };
        }
      );

      formatter = forAllSystems (system: nixpkgs.legacyPackages.${system}.nixfmt);
    };
}
