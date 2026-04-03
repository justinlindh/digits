module.exports = {
  extends: ['@commitlint/config-conventional'],
  ignores: [(message) => /^Merge\b/.test(message)],
};
