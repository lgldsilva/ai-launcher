# Image: manjarolinux/base:latest
FROM manjarolinux/base@sha256:bbf1f1d746f28e138eea610e140d2f28cbb5b7c5da2fbff034b883527aa604e9
# The base image has no pamac. pamac-cli pulls in bubblewrap, which is
# removed again so the scenario installs it. Docker Desktop's kernel has no
# Landlock: pacman's DisableSandbox covers the pacman step, and pamac, which
# ignores that option, skips its sandbox once the alpm user is gone.
# The image default is builder, not root. Compose starts check.sh through
# sudo because the argv under test is the launcher's uid-0 form
# (`pamac install` with no sudo), which writes into system paths. yay and
# paru stay unprivileged: their rows do not wrap the command in sudo.
RUN sed -i '/^\[options\]/a DisableSandbox' /etc/pacman.conf \
 && pacman -Sy --noconfirm --needed pamac-cli sudo \
 && pacman -Rdd --noconfirm bubblewrap \
 && userdel alpm \
 && useradd -m -s /bin/sh builder \
 && printf 'builder ALL=(ALL) NOPASSWD: /usr/bin/sh, /bin/sh\n' > /etc/sudoers.d/builder \
 && chmod 440 /etc/sudoers.d/builder \
 && visudo -cf /etc/sudoers
USER builder
