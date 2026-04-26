// Firmware semantic-release config: full build with artifacts.
//
// Uses tools/semantic-release-squash-expander instead of the standard
// commit-analyzer / release-notes-generator pair so GitHub squash-merge
// commits get expanded into their per-bullet virtual commits before scope
// matching. Without this, a cross-component PR squashed under (e.g.) scope
// "image" would never trigger this firmware-scoped release even if the
// squash body contained "fix(firmware): ..." bullets.
const path = require('path');
const squashExpander = path.resolve(__dirname, '..', 'tools', 'semantic-release-squash-expander.cjs');
// The expander lives outside any project's node_modules and would fail to
// resolve these otherwise; load them here where they're installed and pass
// them through plugin options.
const wrapped = {
  commitAnalyzer: require('@semantic-release/commit-analyzer'),
  releaseNotesGenerator: require('@semantic-release/release-notes-generator'),
};

module.exports = {
  branches: ['main'],
  tagFormat: 'fw/v${version}',
  plugins: [
    [squashExpander, {
      _wrapped: wrapped,
      // Wrapped commit-analyzer options.
      releaseRules: [
        // Scope globs use micromatch substring patterns so multi-scope commits
        // like fix(digitsd,firmware,server) trigger this release too.
        { scope: '*firmware*', type: 'feat', release: 'minor' },
        { scope: '*firmware*', type: 'fix', release: 'patch' },
        { scope: '*firmware*', type: 'perf', release: 'patch' },
        { scope: '*firmware*', breaking: true, release: 'major' },
        { scope: '!*firmware*', release: false },
      ],
      // Shared parser opts (analyzer + notes-generator both honor these).
      parserOpts: {
        noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
      },
      // Wrapped release-notes-generator options.
      preset: 'conventionalcommits',
      writerOpts: {
        // Only include commits whose scope is or contains "firmware".
        // Multi-scope commits like fix(digitsd,firmware,server) are included.
        transform: (commit) => {
          if (!commit.scope || !/(^|,)firmware(,|$)/.test(commit.scope)) return;
          const typeMap = { feat: 'Features', fix: 'Bug Fixes', perf: 'Performance Improvements' };
          if (!typeMap[commit.type]) return;
          return { ...commit, type: typeMap[commit.type], shortHash: commit.hash && commit.hash.substring(0, 7) };
        },
      },
    }],
    ['@semantic-release/exec', {
      prepareCmd: 'bash ../tools/build-firmware.sh ${nextRelease.version}',
    }],
    ['@semantic-release/github', {
      assets: [
        { path: '../artifacts/firmware-*.elf', label: 'Pico firmware (ELF)' },
        { path: '../artifacts/firmware-*.elf.sha256', label: 'Firmware SHA256 checksum' },
      ],
      successComment: false,
      failComment: false,
    }],
  ],
};
