theory Audit
  imports Contract
begin

ML \<open>
  val target = @{thm sum_acc_correct};
  val dependencies = Thm_Deps.all_oracles [target];
  if null dependencies
  then writeln "ORACLE_AUDIT_OK sum_acc_correct"
  else error "ORACLE_AUDIT_FAILED sum_acc_correct"
\<close>

end
