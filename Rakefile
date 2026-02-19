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

directory "tmp/test"

namespace :build do
  task :directory do
    Dir.mkdir("build") unless Dir.exist?("build")
  end

  desc "Build assets"
  task assets: :directory do
    sh "npm run build"
    Dir.glob("build/assets/**/*.{js,css,html}").each do |path|
      sh "zopfli", path
    end
  end

  desc "Build logger4life binary"
  task binary: ["build/logger4life"]
end

file "build/logger4life" => ["build:directory", *FileList["backend/*.go"]] do |t|
  sh "go build -o build/logger4life"
end

file "build/logger4life-linux" => ["build:directory", *FileList["backend/*.go"]] do |t|
  sh "cd backend; GOOS=linux GOARCH=amd64 go build -o ../build/logger4life-linux github.com/jackc/logger4life/backend"
end

desc "Build all"
task build: ["build:assets", "build:binary", "build/logger4life-linux"]

desc "Run logger4life"
task run: "build:binary" do
  puts "Remember to start vite dev server"
  exec "build/logger4life server --config logger4life.conf"
end

desc "Watch for source changes and rebuild and rerun"
task :rerun do
  exec %q[watchexec --project-origin . -r -f Rakefile -f main.go -f "backend/**" -- rake run]
end

file "tmp/test/.databases-prepared" => FileList["postgresql/**/*.sql", "test/testdata/*.sql"] do
  sh "psql -f test/setup_test_databases.sql > /dev/null"
  sh "PGDATABASE=logger4life_test tern migrate -m postgresql/migrations -c postgresql/tern.conf"
  sh "touch tmp/test/.databases-prepared"
end

desc "Perform all preparation necessary to run tests"
task "test:prepare" => ["tmp/test", "tmp/test/.databases-prepared"]

desc "Run Go tests"
task "test:backend" => ["test:prepare"] do
  sh "go test ./..."
end

desc "Run Playwright browser tests"
task "test:browser" => ["test:prepare"] do
  sh "npm test"
end

desc "Run all tests"
task test: ["test:backend", "test:browser"]

task default: :test
