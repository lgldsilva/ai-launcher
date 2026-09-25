# Image: archlinux:base-20260920.0.596911
FROM archlinux@sha256:f3691b4dde62ba4c4b6f0ae2c1fbf28e8c0c8c4b9a35c7e06dc1f70e21aa29f6
# Docker Desktop's kernel has no Landlock, so pacman's sandbox cannot start.
RUN sed -i '/^\[options\]/a DisableSandbox' /etc/pacman.conf \
 && pacman -Sy --noconfirm --needed base-devel git sudo \
 && useradd -m builder \
 && echo 'builder ALL=(ALL) NOPASSWD: ALL' >> /etc/sudoers \
 && su builder -c 'git clone https://aur.archlinux.org/yay.git /tmp/helper && cd /tmp/helper && makepkg -si --noconfirm'
USER builder
