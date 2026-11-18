RD client for Windows (Server 2022 / Windows 11)

Files
  rd\                   the client (RD.exe, forked RustDesk)
  install.ps1           silent unattended install + enrolment
  test-connection.ps1   checks this machine can reach the RD server

1. Test the network (optional, recommended once per site):
     powershell -ExecutionPolicy Bypass -File test-connection.ps1

2. Install (as Administrator or SYSTEM):
     powershell -ExecutionPolicy Bypass -File install.ps1 -Token rde_xxx -Group "Office/Rack 1"

   Get a token on the server:
     docker compose exec api rd-api token create --group "Office/Rack 1" --max-uses 20 --expires-hours 72

   A log is written to C:\ProgramData\RD\install.log.

3. As a viewer: start RD from the Start menu, log in (Account > Login) with your
   admin account + authenticator code. Your devices appear in the address book,
   tagged by location, and connect without a password.
