// Sign-in screen: account password, then the authenticator code.
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_hbb/common.dart';
import 'package:flutter_hbb/common/hbbs/hbbs.dart';
import 'package:flutter_hbb/models/platform_model.dart';
import 'package:flutter_hbb/models/user_model.dart';

import 'theme.dart';

class RdSignInPage extends StatefulWidget {
  const RdSignInPage({Key? key}) : super(key: key);

  @override
  State<RdSignInPage> createState() => _RdSignInPageState();
}

class _RdSignInPageState extends State<RdSignInPage> {
  final _user = TextEditingController();
  final _pass = TextEditingController();
  final _code = TextEditingController();
  final _codeFocus = FocusNode();
  bool _codeStep = false;
  bool _busy = false;
  String? _error;
  String? _secret;
  String? _tfaUser;
  String _server = '';

  @override
  void initState() {
    super.initState();
    bind.mainGetApiServer().then((v) {
      if (mounted) {
        setState(() => _server = Uri.tryParse(v)?.host ?? v);
      }
    });
  }

  @override
  void dispose() {
    _user.dispose();
    _pass.dispose();
    _code.dispose();
    _codeFocus.dispose();
    super.dispose();
  }

  Future<void> _finish(LoginResponse resp) async {
    await bind.mainSetLocalOption(
        key: 'access_token', value: resp.access_token!);
    await bind.mainSetLocalOption(
        key: 'user_info', value: jsonEncode(resp.user ?? {}));
    await UserModel.updateOtherModels();
  }

  Future<void> _submitPassword() async {
    if (_busy) return;
    if (_user.text.trim().isEmpty || _pass.text.isEmpty) {
      setState(() => _error = 'Enter your username and password.');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final resp = await gFFI.userModel.login(LoginRequest(
          username: _user.text.trim(),
          password: _pass.text,
          id: await bind.mainGetMyId(),
          uuid: await bind.mainGetUuid(),
          autoLogin: true,
          type: HttpType.kAuthReqTypeAccount));
      if (resp.type == HttpType.kAuthResTypeToken &&
          resp.access_token != null) {
        await _finish(resp);
        return;
      }
      if (resp.type == HttpType.kAuthResTypeEmailCheck &&
          resp.tfa_type == HttpType.kAuthResTypeTfaCheck) {
        setState(() {
          _codeStep = true;
          _secret = resp.secret;
          _tfaUser = resp.user?.name ?? _user.text.trim();
          _code.clear();
        });
        _codeFocus.requestFocus();
      } else {
        setState(() => _error = 'The server answered in a way RD does not '
            'understand. Check that this is the RD server.');
      }
    } on RequestException catch (e) {
      setState(() => _error = e.cause.isEmpty
          ? 'Sign-in failed (HTTP ${e.statusCode}).'
          : e.cause);
    } catch (e) {
      setState(() =>
          _error = 'Could not reach the server. Check your connection. ($e)');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _submitCode() async {
    if (_busy) return;
    final code = _code.text.trim();
    if (code.length != 6) {
      setState(() => _error = 'The code has 6 digits.');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final resp = await gFFI.userModel.login(LoginRequest(
          verificationCode: code,
          tfaCode: code,
          secret: _secret,
          username: _tfaUser,
          id: await bind.mainGetMyId(),
          uuid: await bind.mainGetUuid(),
          autoLogin: true,
          type: HttpType.kAuthReqTypeEmailCode));
      if (resp.type == HttpType.kAuthResTypeToken &&
          resp.access_token != null) {
        await _finish(resp);
        return;
      }
      setState(() => _error = "That code didn't match. Codes change every "
          '30 seconds; try the current one.');
    } on RequestException catch (e) {
      setState(() => _error = e.cause.isEmpty
          ? "That code didn't match. Codes change every 30 seconds; try the current one."
          : e.cause);
    } catch (e) {
      setState(() =>
          _error = 'Could not reach the server. Check your connection. ($e)');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Widget _label(String text) => Padding(
        padding: const EdgeInsets.only(bottom: 6),
        child: Text(text,
            style: const TextStyle(
                fontSize: 14, fontWeight: FontWeight.w500, color: Rd.ink)),
      );

  Widget _passwordStep() => Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const Text('Sign in to RD',
              style: TextStyle(
                  fontSize: 26, fontWeight: FontWeight.w600, color: Rd.ink)),
          const SizedBox(height: 6),
          Text.rich(TextSpan(children: [
            const TextSpan(text: 'Server: '),
            TextSpan(
                text: _server,
                style: const TextStyle(fontFamily: Rd.mono, color: Rd.ink)),
          ]),
              style: const TextStyle(fontSize: 15, color: Rd.muted)),
          const SizedBox(height: 24),
          _label('Username'),
          TextField(
            controller: _user,
            autofocus: true,
            style: const TextStyle(fontSize: 16),
            decoration: Rd.field(null),
            textInputAction: TextInputAction.next,
          ),
          const SizedBox(height: 16),
          _label('Password'),
          TextField(
            controller: _pass,
            obscureText: true,
            style: const TextStyle(fontSize: 16),
            decoration: Rd.field(null),
            onSubmitted: (_) => _submitPassword(),
          ),
          const SizedBox(height: 24),
          ElevatedButton(
            style: Rd.primary.copyWith(
                minimumSize: const MaterialStatePropertyAll(Size(0, 48))),
            onPressed: _busy ? null : _submitPassword,
            child: Text(_busy ? 'Signing in…' : 'Continue'),
          ),
        ],
      );

  Widget _codeStepView() => Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const Text('Enter your code',
              style: TextStyle(
                  fontSize: 26, fontWeight: FontWeight.w600, color: Rd.ink)),
          const SizedBox(height: 6),
          const Text(
              'Open your authenticator app and type the 6-digit code for RD.',
              style: TextStyle(fontSize: 15, height: 1.5, color: Rd.muted)),
          const SizedBox(height: 24),
          _label('Code'),
          TextField(
            controller: _code,
            focusNode: _codeFocus,
            keyboardType: TextInputType.number,
            inputFormatters: [
              FilteringTextInputFormatter.digitsOnly,
              LengthLimitingTextInputFormatter(6),
            ],
            style: const TextStyle(
                fontFamily: Rd.mono, fontSize: 26, letterSpacing: 8),
            decoration: Rd.field('123456'),
            onSubmitted: (_) => _submitCode(),
          ),
          const SizedBox(height: 24),
          ElevatedButton(
            style: Rd.primary.copyWith(
                minimumSize: const MaterialStatePropertyAll(Size(0, 48))),
            onPressed: _busy ? null : _submitCode,
            child: Text(_busy ? 'Checking…' : 'Sign in'),
          ),
          const SizedBox(height: 8),
          TextButton(
            onPressed: _busy
                ? null
                : () => setState(() {
                      _codeStep = false;
                      _error = null;
                    }),
            style: TextButton.styleFrom(foregroundColor: Rd.fjord),
            child: const Text('Back'),
          ),
        ],
      );

  @override
  Widget build(BuildContext context) {
    return Container(
      color: Rd.mist,
      alignment: Alignment.center,
      child: SingleChildScrollView(
        padding: const EdgeInsets.all(24),
        child: Container(
          width: 400,
          padding: const EdgeInsets.fromLTRB(36, 40, 36, 36),
          decoration: BoxDecoration(
            color: Colors.white,
            borderRadius: BorderRadius.circular(14),
            border: Border.all(color: Rd.line),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            mainAxisSize: MainAxisSize.min,
            children: [
              Align(alignment: Alignment.centerLeft, child: Rd.mark(56)),
              const SizedBox(height: 24),
              _codeStep ? _codeStepView() : _passwordStep(),
              if (_error != null) ...[
                const SizedBox(height: 16),
                Text(_error!,
                    style: const TextStyle(fontSize: 14, color: Rd.danger)),
              ],
            ],
          ),
        ),
      ),
    );
  }
}
