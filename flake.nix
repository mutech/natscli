{
  # Fork-local dev shell (mutech/natscli). Not part of upstream nats-io/natscli.
  #
  # Tooling mirrors .github/workflows/test.yaml so local runs match CI:
  #   - Go: the lint job uses 1.27, the test/FIPS jobs a matrix of 1.26 and 1.27
  #     (`go-version:` "1.26"/"1.27" = newest patch of each). go_1_26 and go_1_27
  #     from the locked nixpkgs; keep the lock current (`nix flake update`).
  #   - staticcheck and misspell: CI runs `go install …@latest`, which has no
  #     version to read. The pins below are those `@latest` values frozen at the
  #     last sync (`go list -m <module>@latest`); the qa lint target installs them
  #     the same way CI does, built with the lint job's Go. Re-resolve on every
  #     sync (own-dependency management).
  #       honnef.co/go/tools v0.8.2 (staticcheck)  resolved 2026-11-20
  #       github.com/client9/misspell v0.3.4       resolved 2026-11-20 (archived upstream)
  #
  # `qa fast|full|list` runs what CI runs; see qa below.
  description = "natscli (snats fork) dev shell, tool versions pinned to CI";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};

      staticcheckVersion = "v0.8.2";
      misspellVersion = "v0.3.4";

      goLint = pkgs.go_1_27;
      goMatrix = { "1.26" = pkgs.go_1_26; "1.27" = pkgs.go_1_27; };

      rerereTrain = pkgs.writeShellScriptBin "rerere-train" ''
        exec ${pkgs.bash}/bin/bash ${pkgs.git}/share/git/contrib/rerere-train.sh "$@"
      '';

      # QA runner. Every target's full output goes to its own log, wall time and
      # exit code to summary.tsv and to the cross-session timings file. No
      # timeouts. All targets run even if one fails.
      #   qa fast   lint + build + unit packages (Go 1.27)
      #   qa full   lint, then build + all tests + FIPS tests for Go 1.26 and 1.27
      qa = pkgs.writeShellApplication {
        name = "qa";
        excludeShellChecks = [ "SC2329" ]; # targets are dispatched via "t_$name"
        runtimeInputs = [ pkgs.coreutils pkgs.gnugrep pkgs.findutils pkgs.git ];
        text = ''
          root="$(git rev-parse --show-toplevel)"
          cd "$root"
          export GOTOOLCHAIN=local
          go_for() { case "$1" in 1.26) echo ${goMatrix."1.26"}/bin ;; 1.27) echo ${goMatrix."1.27"}/bin ;; esac; }
          use_go() { local bin; bin="$(go_for "$1")"; PATH="$bin:$PATH"; export PATH; go version; }
          pkgs_all() { go list ./... | grep -F -e asciigraph -v; }

          # As the CI lint job: go fmt must change nothing, vet, misspell, staticcheck.
          t_lint() {
            export GOBIN="''${GOBIN:-$root/.devshell/bin}"
            export PATH="${goLint}/bin:$GOBIN:$PATH"; go version
            go install honnef.co/go/tools/cmd/staticcheck@${staticcheckVersion}
            go install github.com/client9/misspell/cmd/misspell@${misspellVersion}
            mapfile -t list < <(pkgs_all)
            changed=$(go fmt "''${list[@]}" | wc -l); [ "$changed" -eq 0 ] || { echo "go fmt changed $changed files"; return 1; }
            go vet -composites=false "''${list[@]}"
            find . -type f -name '*.go' -not -path './.devshell/*' | grep -F -e asciigraph -v | xargs misspell -error -locale US
            staticcheck -f stylish "''${list[@]}"
          }
          # As CI: the binary lands at nats/nats (gitignored); the FIPS tests run ../nats.
          build()  { use_go "$1"; (cd nats && go build); }
          tests()  { use_go "$1"; pkgs_all | xargs go test -v --failfast -p=1; }
          fips()   { use_go "$1"; (cd nats/tests && CI=true go test -tags fips -v --failfast -p=1 -run FIPS .); }
          unit()   { use_go 1.27; pkgs_all | grep -v '/nats/tests$' | xargs go test -v --failfast -p=1; }

          t_build_1_27() { build 1.27; }
          t_build_1_26() { build 1.26; }
          t_unit()       { unit; }
          t_tests_1_27() { tests 1.27; }
          t_tests_1_26() { tests 1.26; }
          t_fips_1_27()  { fips 1.27; }
          t_fips_1_26()  { fips 1.26; }

          fast_targets="lint build_1_27 unit"
          full_targets="lint build_1_26 tests_1_26 fips_1_26 build_1_27 tests_1_27 fips_1_27"

          stage="''${1:-}"; shift || true
          case "$stage" in
            fast) targets="''${*:-$fast_targets}" ;;
            full) targets="''${*:-$full_targets}" ;;
            list) echo "fast: $fast_targets"; echo "full: $full_targets"; exit 0 ;;
            *) echo "usage: qa fast|full|list [TARGET...]" >&2; exit 2 ;;
          esac

          rev="$(git rev-parse --short HEAD)$(git diff --quiet || echo +dirty)"
          # Logs and timings live outside the tree (linters such as codespell walk
          # it), next to each other under the upstream-sync state dir.
          state="''${XDG_STATE_HOME:-$HOME/.local/state}/snats-upstream-sync"
          dir="$state/qa/natscli/$(date -u +%Y%m%dT%H%M%SZ)-$stage"
          timings="$state/timings.tsv"
          mkdir -p "$dir" "$(dirname "$timings")"
          [ -s "$timings" ] || printf 'date\trepo\trev\tstage\ttarget\tseconds\trc\thost\n' > "$timings"
          echo "qa $stage @ $rev -> $dir"
          failed=0
          for t in $targets; do
            start=$(date +%s); set +e
            ( set -x; "t_$t" ) > "$dir/$t.log" 2>&1; rc=$?
            set -e; secs=$(( $(date +%s) - start ))
            printf '%s\t%s\t%s\n' "$t" "$secs" "$rc" >> "$dir/summary.tsv"
            printf '%s\tnatscli\t%s\t%s\t%s\t%s\t%s\t%s\n' "$(date -u +%FT%TZ)" "$rev" "$stage" "$t" "$secs" "$rc" "$(hostname)" >> "$timings"
            printf '  %-24s rc=%-3s %6ss\n' "$t" "$rc" "$secs"
            [ "$rc" -eq 0 ] || failed=1
          done
          echo "logs: $dir"
          exit "$failed"
        '';
      };
    in {
      devShells.${system}.default = pkgs.mkShell {
        packages = [ goLint pkgs.git rerereTrain qa ];

        shellHook = ''
          cache="''${XDG_CACHE_HOME:-$HOME/.cache}/nats-fork-devshell"
          export GOPATH="$cache/go"
          export GOMODCACHE="$cache/go/pkg/mod"
          export GOCACHE="$cache/go-build"
          export GOBIN="$cache/natscli-bin"
          echo "natscli dev shell: go ${goMatrix."1.26".version} / ${goMatrix."1.27".version}, staticcheck ${staticcheckVersion}, misspell ${misspellVersion}"
        '';
      };
    };
}
