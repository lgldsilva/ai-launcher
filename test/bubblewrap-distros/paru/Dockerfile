FROM archlinux:latest
RUN pacman -Sy --noconfirm --needed base-devel git sudo \
 && useradd -m builder \
 && echo 'builder ALL=(ALL) NOPASSWD: ALL' >> /etc/sudoers \
 && su builder -c 'git clone https://aur.archlinux.org/paru.git /tmp/helper && cd /tmp/helper && makepkg -si --noconfirm'
USER builder
