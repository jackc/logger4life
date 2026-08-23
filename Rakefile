begin
  require "bundler"
  Bundler.setup
rescue LoadError
  puts "You must `gem install bundler` and `bundle install` to run rake tasks"
end

require "rake/clean"
require "fileutils"
require "erb"

CLOBBER.include("build")

# Build matrix: all OS/arch combinations
BUILD_TARGETS = [
  {os: "linux", arch: "amd64"},
  {os: "linux", arch: "arm64"},
  {os: "darwin", arch: "amd64"},
  {os: "darwin", arch: "arm64"},
].freeze

GO_SOURCES = FileList["Rakefile", "*.go", "go.*", "**/*.go"].exclude(/_test.go$/)
ASSET_SOURCES = FileList["src/**/*", "package.json", "vite.config.js"]

directory "tmp/test"

# This task is for development convenience - it builds the binary for the current platform.
# The build matrix tasks below are for CI and release builds.
file "build/logger4life" => GO_SOURCES do |t|
  mkdir_p "build"
  sh "go build -o build/logger4life"
end

# Asset build with tracking file for dependency management
file "build/assets/.built" => ASSET_SOURCES do
  sh "npm run build"
  touch "build/assets/.built"
end

# Generate file tasks for each target
BUILD_TARGETS.each do |target|
  dir = "build/#{target[:os]}_#{target[:arch]}"
  binary = "#{dir}/logger4life"
  assets_dir = "#{dir}/assets"

  # Binary depends on Go sources
  file binary => GO_SOURCES do |t|
    mkdir_p dir
    sh "GOOS=#{target[:os]} GOARCH=#{target[:arch]} go build -o #{t.name}"
  end

  # Assets copy depends on asset build
  file "#{assets_dir}/.copied" => "build/assets/.built" do |t|
    rm_rf assets_dir
    cp_r "build/assets", assets_dir
    touch t.name
  end

  # VERSION file with git commit hash
  version_file = "#{dir}/VERSION"
  task version_file do |t|
    mkdir_p dir
    commit = `git rev-parse HEAD`.chomp
    dirty = `git status --porcelain`.strip.empty? ? "" : "-dirty"
    File.write(t.name, "#{commit}#{dirty}\n")
  end

  # Convenience task for full build directory
  desc "Build artifact for #{target[:os]}/#{target[:arch]}"
  task dir => [binary, "#{assets_dir}/.copied", version_file]

  # Tarball of the release directory
  tarball = "#{dir}.tar.gz"
  file tarball => dir do |t|
    sh "tar -czf #{t.name} -C #{dir} ."
  end
end

namespace :build do
  desc "Build assets"
  task assets: "build/assets/.built"

  desc "Build logger4life binary"
  task binary: ["build/logger4life"]
end

desc "Build all"
task build: ["build/logger4life", "build/assets/.built"]

file "tmp/test/.databases-prepared" => FileList["Rakefile", ".mise.toml", "postgresql/**/*.sql", "test/*.sql", "test/testdata/*.sql"] do
  # devports may have created .dev/ports.env during test:prepare, after this
  # Rake process started. A nested mise exec reloads that new environment.
  sh "mise exec -- psql --no-psqlrc -v ON_ERROR_STOP=1 -f test/setup_test_databases.sql > /dev/null"
  sh "touch tmp/test/.databases-prepared"
end

desc "Perform all preparation necessary to run tests"
task "test:prepare" => ["tmp/test"] do
  # The tests need this worktree's PostgreSQL cluster, but not the rest of the
  # development stack. Starting it here is what lets `rake test` work whether
  # or not `mise run dev` is up. It is invoked rather than declared as a
  # prerequisite so that the file task keeps its own freshness check.
  sh "scripts/db-ensure-running"
  Rake::Task["tmp/test/.databases-prepared"].invoke
end

desc "Run Go tests"
task "test:backend" => ["test:prepare"] do
  sh "mise exec -- go test ./..."
end

desc "Run Playwright browser tests"
task "test:browser" => ["test:prepare"] do
  sh "mise exec -- npm test"
end

desc "Run all tests"
task test: ["test:backend", "test:browser"]

task default: :test
