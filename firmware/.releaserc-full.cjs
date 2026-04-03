// firmware/.releaserc.cjs — semantic-release config for Pico firmware
module.exports = {
  branches: ['main'],
  tagFormat: 'fw/v${version}',
  plugins: [
    [
      '@semantic-release/commit-analyzer',
      {
        releaseRules: [
          { scope: 'firmware', type: 'feat', release: 'minor' },
          { scope: 'firmware', type: 'fix', release: 'patch' },
          { scope: 'firmware', type: 'perf', release: 'patch' },
          { scope: 'firmware', breaking: true, release: 'major' },
          // Commits without firmware scope never bump firmware version
          { scope: '!firmware', release: false },
        ],
        parserOpts: {
          noteKeywords: ['BREAKING CHANGE', 'BREAKING CHANGES'],
        },
      },
    ],
    '@semantic-release/release-notes-generator',
    [
      '@semantic-release/exec',
      {
        prepareCmd: 'bash ../tools/build-firmware.sh ${nextRelease.version}',
      },
    ],
    [
      '@saithodev/semantic-release-gitea',
      {
        giteaUrl: process.env.GITEA_URL,
        assets: [
          {
            path: '../artifacts/firmware.elf',
            label: 'Pico firmware (ELF)',
          },
          {
            path: '../artifacts/firmware.elf.sha256',
            label: 'Firmware SHA256 checksum',
          },
        ],
      },
    ],
  ],
};
