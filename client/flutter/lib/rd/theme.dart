// RD look: colours and button styles shared by the RD screens.
import 'package:flutter/material.dart';
import 'package:flutter_svg/flutter_svg.dart';

class Rd {
  Rd._();

  static const Color ink = Color(0xFF16262B);
  static const Color inkRaised = Color(0xFF24373D);
  static const Color fjord = Color(0xFF0F6E74);
  static const Color fjordDark = Color(0xFF0A5A5F);
  static const Color signal = Color(0xFFF2B134);
  static const Color mist = Color(0xFFEEF2F1);
  static const Color mistDark = Color(0xFFDCE6E4);
  static const Color line = Color(0xFFD5DDDB);
  static const Color lineStrong = Color(0xFFC5D0CE);
  static const Color muted = Color(0xFF4D5E63);
  static const Color body = Color(0xFF33454A);
  static const Color onInkMuted = Color(0xFFB9C6C4);
  static const Color online = Color(0xFF1E8449);
  static const Color offline = Color(0xFF93A0A3);
  static const Color danger = Color(0xFFA3342B);
  static const Color selected = Color(0xFFE3EFEE);

  static const String sans = 'IBMPlexSans';
  static const String mono = 'IBMPlexMono';

  static const TextStyle monoText =
      TextStyle(fontFamily: mono, fontSize: 14, color: body);

  static final ButtonStyle primary = ElevatedButton.styleFrom(
    backgroundColor: fjord,
    foregroundColor: Colors.white,
    disabledBackgroundColor: lineStrong,
    elevation: 0,
    minimumSize: const Size(0, 44),
    padding: const EdgeInsets.symmetric(horizontal: 18),
    shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
    textStyle: const TextStyle(
        fontFamily: sans, fontSize: 15, fontWeight: FontWeight.w600),
  );

  static final ButtonStyle secondary = OutlinedButton.styleFrom(
    foregroundColor: ink,
    backgroundColor: Colors.white,
    side: const BorderSide(color: lineStrong),
    minimumSize: const Size(0, 40),
    padding: const EdgeInsets.symmetric(horizontal: 14),
    shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
    textStyle: const TextStyle(
        fontFamily: sans, fontSize: 14, fontWeight: FontWeight.w500),
  );

  static final ButtonStyle dangerOutline = OutlinedButton.styleFrom(
    foregroundColor: danger,
    backgroundColor: Colors.white,
    side: const BorderSide(color: Color(0xFFC9A29D)),
    minimumSize: const Size(0, 36),
    padding: const EdgeInsets.symmetric(horizontal: 14),
    shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(8)),
    textStyle: const TextStyle(
        fontFamily: sans, fontSize: 14, fontWeight: FontWeight.w600),
  );

  static InputDecoration field(String? hint) => InputDecoration(
        hintText: hint,
        isDense: true,
        filled: true,
        fillColor: Colors.white,
        contentPadding:
            const EdgeInsets.symmetric(horizontal: 12, vertical: 12),
        border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(8),
            borderSide: const BorderSide(color: Color(0xFFA9B6B3))),
        enabledBorder: OutlineInputBorder(
            borderRadius: BorderRadius.circular(8),
            borderSide: const BorderSide(color: Color(0xFFA9B6B3))),
        focusedBorder: OutlineInputBorder(
            borderRadius: BorderRadius.circular(8),
            borderSide: const BorderSide(color: fjord, width: 2)),
      );

  /// The Pointer mark.
  static Widget mark(double size) =>
      SvgPicture.asset('assets/icon.svg', width: size, height: size);

  /// Solid dot for online, hollow ring for offline (not colour alone).
  static Widget statusDot(bool online, {double size = 10}) => Semantics(
        label: online ? 'Online' : 'Offline',
        child: Container(
          width: size,
          height: size,
          decoration: BoxDecoration(
            shape: BoxShape.circle,
            color: online ? Rd.online : Colors.transparent,
            border: online ? null : Border.all(color: offline, width: 2),
          ),
        ),
      );
}
