# Image: manjarolinux/base:latest
FROM manjarolinux/base@sha256:bbf1f1d746f28e138eea610e140d2f28cbb5b7c5da2fbff034b883527aa604e9
# The base image has no pamac. pamac-cli pulls in bubblewrap, which is
# removed again so the scenario installs it. Docker Desktop's kernel has no
# Landlock: pacman's DisableSandbox covers the pacman step, and pamac, which
# ignores that option, skips its sandbox once the alpm user is gone. The
# scenario runs as a normal user, so the argv is the launcher's sudo -n form.
RUN sed -i '/^\[options\]/a DisableSandbox' /etc/pacman.conf \
 && pacman -Sy --noconfirm --needed pamac-cli \
 && pacman -Rdd --noconfirm bubblewrap \
 && userdel alpm \
 && pacman -S --noconfirm --needed sudo \
 && useradd -m bwrapcheck \
 && echo 'bwrapcheck ALL=(ALL) NOPASSWD: ALL' >> /etc/sudoers
USER bwrapcheck
