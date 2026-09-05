#!/usr/bin/env ruby

require 'yaml'

root = File.expand_path('..', __dir__)
load_workflow = ->(name) { YAML.load_file(File.join(root, '.github', 'workflows', name)) }
mirror = load_workflow.call('dockerhub.yml')
edge = load_workflow.call('container.yml')
release = load_workflow.call('release.yml')

def require_contract(value, message)
  abort(message) unless value
end

require_contract(mirror['permissions'] == { 'contents' => 'read' }, 'mirror must not need GitHub write permissions')
proof = mirror.fetch('jobs').fetch('prove')
require_contract(proof['needs'] == 'mirror', 'proof must depend on the exact mirrored digest')
require_contract(proof.dig('strategy', 'matrix', 'platform').sort == %w[linux/amd64 linux/arm64], 'both Docker Hub platforms require anonymous proofs')
require_contract(proof['permissions'] == { 'contents' => 'read' }, 'anonymous proof permissions drifted')
require_contract(!proof['steps'].any? { |step| step['uses'].to_s.include?('login-action') || step.to_s.include?('secrets.') }, 'proof must not receive registry credentials')
require_contract(release.dig('jobs', 'publish', 'needs').include?('dockerhub'), 'Docker Hub proof must gate GitHub Release')
require_contract(release.dig('jobs', 'dockerhub', 'needs').sort == %w[prove release], 'mirror must consume verified GHCR release outputs')
require_contract(edge.dig('jobs', 'dockerhub', 'needs') == 'publish', 'edge mirror must consume the successful GHCR build')
[edge, release].each do |workflow|
  job = workflow.dig('jobs', 'dockerhub')
  require_contract(job['uses'] == './.github/workflows/dockerhub.yml', 'publishers must share the same mirror workflow')
  require_contract(job['secrets'].keys == ['DOCKERHUB_TOKEN'], 'only the Docker Hub token may be delegated')
end
steps = mirror.dig('jobs', 'mirror', 'steps')
identity = steps.find { |step| step['id'] == 'identity' }
require_contract(identity['run'].include?('test "$GITHUB_REF" = refs/heads/main'), 'manual backfill must use main workflow code')
require_contract(identity['run'].include?('gh release download'), 'manual backfill must read the published release digest')
require_contract(identity.dig('env', 'DOCKERHUB_USERNAME').include?('vars.DOCKERHUB_USERNAME'), 'public image paths must not use a masked username secret')
require_contract(steps.none? { |step| step['uses'].to_s.include?('build-push-action') }, 'mirror must copy verified manifests without rebuilding')
puts 'dockerhub_workflow_contract=passed release_gate=true anonymous_architectures=2 manual_release_identity=true least_privilege=true'
