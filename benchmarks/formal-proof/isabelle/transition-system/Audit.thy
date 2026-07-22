theory Audit
  imports Contract
begin

ML \<open>
  val target = @{thm run_busy_closed_form};
  val dependencies = Thm_Deps.all_oracles [target];
  if null dependencies
  then writeln "ORACLE_AUDIT_OK run_busy_closed_form"
  else error "ORACLE_AUDIT_FAILED run_busy_closed_form"
\<close>

end
