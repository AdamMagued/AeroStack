class Aerostack < Formula
  desc "Ultra-lightweight Linux microVM and Docker engine for macOS"
  homepage "https://github.com/AdamMagued/AeroStack"
  version "0.1.0"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/AdamMagued/AeroStack/releases/download/v0.1.0/aerostack-v0.1.0-darwin-arm64.tar.gz"
      sha256 "d9919dcc233076a5b9ba4f0faa4ed5459fb41befeededcd0ff9d25af38490752"

      resource "guest_assets" do
        url "https://github.com/AdamMagued/AeroStack/releases/download/v0.1.0/aerostack-v0.1.0-linux-arm64.tar.gz"
        sha256 "d9277f3af043204bd986e96db0e099477592d1cbf42abdb2fba466140faa5404"
      end
    end
  end

  depends_on :macos
  depends_on arch: :arm64

  def install
    bin.install "aerostack"
    bin.install "aero-vmd"
    bin.install "aero-bridge"
    bin.install "aero-netd"

    # Symlinks
    bin.install_symlink "aerostack" => "aero"
    bin.install_symlink "aerostack" => "aeroctl"
    bin.install_symlink "aerostack" => "orb"
    bin.install_symlink "aerostack" => "orbctl"
    bin.install_symlink "aero-vmd" => "orb-vmd"
    bin.install_symlink "aero-bridge" => "orb-docker-bridge"
    bin.install_symlink "aero-netd" => "orb-netd"

    # Codesign aero-vmd with entitlements if present
    if File.exist?("entitlements.plist")
      system "codesign", "--force", "--sign", "-", "--entitlements", "entitlements.plist", bin/"aero-vmd"
    end

    # Install guest assets into share/aerostack
    (share/"aerostack").mkpath
    resource("guest_assets").stage do
      (share/"aerostack").install "vmlinux", "initramfs-virt", "rootfs.ext4", "aero-agent"
    end
  end

  def post_install
    cache_dir = Pathname.new(Dir.home)/".aerostack/cache"
    cache_dir.mkpath
    %w[vmlinux initramfs-virt rootfs.ext4 aero-agent].each do |asset|
      src = share/"aerostack"/asset
      dst = cache_dir/asset
      cp src, dst unless dst.exist?
    end
    (cache_dir/"orb-agent").make_symlink(cache_dir/"aero-agent") unless (cache_dir/"orb-agent").exist?
  end

  test do
    assert_match "aerostack version", shell_output("#{bin}/aerostack version")
    assert_match "aero version", shell_output("#{bin}/aero version")
    assert_match "orbctl version", shell_output("#{bin}/orbctl version")
  end
end
