// Firmware semantic-release config — full build with artifacts
const isGitea = !!process.env.GITEA_URL;

const publishPlugin = isGitea
  ? ['@saithodev/semantic-release-gitea', {
      giteaUrl: process.env.GITEA_URL,
      assets: [
        { path: '../artifacts/firmware-*.elf', label: 'Pico firmware (ELF)' },
        { path: '../artifacts/firmware-*.elf.sha256', label: 'Firmware SHA256 checksum' },
      ],
    }]
  : ['@semantic-release/github', {
      assets: [
        { path: '../artifacts/firmware-*.elf', label: 'Pico firmware (ELF)' },
        { path: '../artifacts/firmware-*.elf.sha256', label: 'Firmware SHA256 checksum' },
      ],
      successComment: false,
      failComment: false,
    }];

module.exports = {
  branches: ['main'],
  tagFormat: 'fw/v${version}',
  plugins: [
    ['@semantic-release/commit-analyzer', {
      releaseRules: [
        { scope: 'firmware', type: 'feat', release: 'minor' },
        { scope: 'firmware', type: 'fix', release: 'patch' },
        { scope: 'firmware', type: 'perf', release: 'patch' },
        { scope: 'firmware', breaking: true, release: 'major' },
        { scope: '!firmware', release: false },
      ],
      parserOpts: {
        noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
      },
    }],
    '@semantic-release/release-notes-generator',
    ['@semantic-release/exec', {
      prepareCmd: 'bash ../tools/build-firmware.sh ${nextRelease.version}',
    }],
    publishPlugin,
  ],
};
