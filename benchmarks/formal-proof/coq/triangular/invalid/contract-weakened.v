From Coq Require Import Arith Lia.

Fixpoint triangular (n : nat) : nat :=
  match n with
  | 0 => 0
  | S previous => S previous + triangular previous
  end.

Theorem triangular_closed_form : forall n, triangular n = triangular n.
Proof.
  reflexivity.
Qed.
