// Extract the protocol's prompt data from the official ZCode v3.14.3 sources.
// Usage: node scripts/extract-vendor-prompt.mjs <vendor-context-dir> <output.json>
import { readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { stripTypeScriptTypes } from 'node:module';
import { runInNewContext } from 'node:vm';
import { createHash } from 'node:crypto';

const [sourceDir, output] = process.argv.slice(2);
if (!sourceDir || !output) throw new Error('vendor context directory and output required');
function section(filename, expressions) {
  const ts = readFileSync(join(sourceDir, filename), 'utf8')
    .replace(/^import[^\n]*\n/gm, '')
    .replace(/^export /gm, '');
  const js = stripTypeScriptTypes(ts, { mode: 'strip' });
  return runInNewContext(`${js}\n;[${expressions.join(',')}]`, { estimateTokens: () => 0 });
}
const [prefix] = section('cli-prefix.ts', ['buildCliPrefixSection().content']);
const [identity] = section('identity.ts', ['buildIdentitySection().content']);
const [desktop] = section('desktop.ts', ['buildDesktopContextSection().content']);
const [before, after] = section('dynamic-sections.ts', [
  'buildDynamicBehaviorSection().content', 'buildContextManagementSection().content',
]);
const [env] = section('env-info.ts', [
  'buildEnvInfoSection({cwd:"{cwd}",isGitRepository:true,platform:"{platform}",shell:"{shell}",osVersion:"{osVersion}"},{providerId:"{provider}",modelId:"{model}"}).content',
]);
const [date] = section('current-date.ts', ['buildCurrentDateSection("{date}").content']);
const builder = readFileSync(join(sourceDir, 'builder.ts'), 'utf8');
const start = builder.indexOf('export function buildContextMetaUserBody(');
const end = builder.indexOf('export function buildSkillsMetaUserBody(', start);
if (start < 0 || end < 0) throw new Error('vendor context builder missing');
const body = stripTypeScriptTypes(builder.slice(start, end).replace(/^export /gm, ''), {mode:'strip'});
const contextText = runInNewContext(`${body}\n;buildContextMetaUserBody([{content:"{section}"}])`, {buildSectionContent: a => a.map(x => x.content).join('\n\n')});
const envLines = env.split('\n');
const contextLines = contextText.split('\n');
const label = (line, placeholder) => line.replace(/^- /, '').replace(`: ${placeholder}`, '');
const result = {
  cliPrefix: prefix,
  stableSections: [identity, desktop],
  beforeEnvironment: before,
  afterEnvironment: after,
  environment: {
    heading: envLines[0], invokedLine: envLines[1],
    cwdLabel: label(envLines[2], '{cwd}'), gitLabel: envLines[3].split(':')[0].replace(/^- /, ''),
    gitNo: 'no', platformLabel: label(envLines[4], '{platform}'),
    shellLabel: label(envLines[5], '{shell}'), osVersionLabel: label(envLines[6], '{osVersion}'),
    poweredByLine: envLines[7],
  },
  context: {
    intro: contextLines[0], outro: contextLines[contextLines.length - 1],
    currentDateHeading: date.split('\n')[0], currentDateLine: date.split('\n')[1],
  },
};
const expected = ['46dd360a22c87a92','3f21ff9a88a03a76','39a1e86c93452c01','bbdc66b399d297ff','1732b7f098a925d7'];
const hashes = [prefix, identity, desktop, before, after].map(x => createHash('sha256').update(x).digest('hex').slice(0,16));
if (JSON.stringify(hashes) !== JSON.stringify(expected)) throw new Error(`vendor prompt hashes do not match: ${hashes}`);
writeFileSync(output, JSON.stringify(result, null, 2) + '\n');
console.log(`official ZCode v3.14.3 prompt hashes: ${hashes.join(' ')}`);
