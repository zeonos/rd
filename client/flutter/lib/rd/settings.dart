// Settings: defaults for new sessions, and the account.
import 'package:flutter/material.dart';
import 'package:flutter_hbb/common.dart';
import 'package:flutter_hbb/common/formatter/id_formatter.dart';
import 'package:flutter_hbb/consts.dart';
import 'package:flutter_hbb/models/platform_model.dart';

import 'theme.dart';

class RdSettingsPage extends StatefulWidget {
  const RdSettingsPage({Key? key}) : super(key: key);

  @override
  State<RdSettingsPage> createState() => _RdSettingsPageState();
}

class _RdSettingsPageState extends State<RdSettingsPage> {
  String _scale = '';
  String _quality = '';
  String _server = '';
  String _myId = '';
  String _version = '';

  @override
  void initState() {
    super.initState();
    _scale = bind.mainGetUserDefaultOption(key: kOptionViewStyle);
    _quality = bind.mainGetUserDefaultOption(key: kOptionImageQuality);
    bind.mainGetApiServer().then((v) {
      if (mounted) setState(() => _server = Uri.tryParse(v)?.host ?? v);
    });
    bind.mainGetMyId().then((v) {
      if (mounted) setState(() => _myId = v);
    });
    bind.mainGetVersion().then((v) {
      if (mounted) setState(() => _version = v);
    });
  }

  Future<void> _set(String key, String value) async {
    await bind.mainSetUserDefaultOption(key: key, value: value);
    setState(() {
      if (key == kOptionViewStyle) _scale = value;
      if (key == kOptionImageQuality) _quality = value;
    });
  }

  Widget _section(List<Widget> children) => Container(
        margin: const EdgeInsets.only(bottom: 20),
        padding: const EdgeInsets.fromLTRB(28, 24, 28, 24),
        decoration: BoxDecoration(
          color: Colors.white,
          borderRadius: BorderRadius.circular(12),
          border: Border.all(color: Rd.line),
        ),
        child: Column(
            crossAxisAlignment: CrossAxisAlignment.start, children: children),
      );

  Widget _radio(String key, String group, String value, String label,
      String hint) {
    return InkWell(
      borderRadius: BorderRadius.circular(8),
      onTap: () => _set(key, value),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 4),
        child: Row(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Radio<String>(
            value: value,
            groupValue: group,
            activeColor: Rd.fjord,
            onChanged: (v) => _set(key, v ?? value),
          ),
          const SizedBox(width: 4),
          Expanded(
            child: Padding(
              padding: const EdgeInsets.only(top: 6),
              child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(label,
                        style: const TextStyle(fontSize: 15, color: Rd.ink)),
                    const SizedBox(height: 2),
                    Text(hint,
                        style:
                            const TextStyle(fontSize: 13, color: Rd.muted)),
                  ]),
            ),
          ),
        ]),
      ),
    );
  }

  Widget _fact(String k, String v, {bool mono = false}) => Padding(
        padding: const EdgeInsets.only(bottom: 10),
        child: Row(children: [
          SizedBox(
              width: 120,
              child: Text(k,
                  style: const TextStyle(fontSize: 15, color: Rd.muted))),
          Text(v,
              style: mono
                  ? const TextStyle(
                      fontFamily: Rd.mono, fontSize: 15, color: Rd.ink)
                  : const TextStyle(fontSize: 15, color: Rd.ink)),
        ]),
      );

  @override
  Widget build(BuildContext context) {
    const h2 = TextStyle(
        fontSize: 19, fontWeight: FontWeight.w600, color: Rd.ink);
    const legend = TextStyle(
        fontSize: 15, fontWeight: FontWeight.w500, color: Rd.ink);
    return Container(
      color: Rd.mist,
      alignment: Alignment.topCenter,
      child: SingleChildScrollView(
        padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 40),
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 720),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text('Settings',
                  style: TextStyle(
                      fontSize: 30,
                      fontWeight: FontWeight.w600,
                      color: Rd.ink)),
              const SizedBox(height: 28),
              _section([
                const Text('New sessions', style: h2),
                const SizedBox(height: 4),
                const Text(
                    'Every session starts like this. You can still change it from the session toolbar.',
                    style: TextStyle(fontSize: 14, color: Rd.muted)),
                const SizedBox(height: 20),
                const Text('Scale', style: legend),
                const SizedBox(height: 6),
                _radio(kOptionViewStyle, _scale, kRemoteViewStyleAdaptive,
                    'Fit window',
                    'The remote screen shrinks or grows with the window. Recommended.'),
                _radio(kOptionViewStyle, _scale, kRemoteViewStyleOriginal,
                    '100%',
                    'One remote pixel per screen pixel. Scroll to see the rest.'),
                const SizedBox(height: 16),
                const Text('Quality', style: legend),
                const SizedBox(height: 6),
                _radio(kOptionImageQuality, _quality, kRemoteImageQualityBest,
                    'Best', 'Sharpest picture. Uses the most bandwidth.'),
                _radio(
                    kOptionImageQuality,
                    _quality,
                    kRemoteImageQualityBalanced,
                    'Balanced',
                    'Good picture on most connections.'),
                _radio(kOptionImageQuality, _quality, kRemoteImageQualityLow,
                    'Fast', 'Lower quality, smoother on slow or busy links.'),
              ]),
              _section([
                const Text('Account', style: h2),
                const SizedBox(height: 16),
                _fact('Signed in as', gFFI.userModel.userName.value),
                _fact('Server', _server, mono: true),
                _fact('This PC', formatID(_myId), mono: true),
                const SizedBox(height: 8),
                OutlinedButton(
                  style: Rd.secondary,
                  onPressed: () => gFFI.userModel.logOut(),
                  child: const Text('Sign out'),
                ),
              ]),
              Text('RD $_version, built on RustDesk',
                  style: const TextStyle(fontSize: 13, color: Rd.muted)),
            ],
          ),
        ),
      ),
    );
  }
}
